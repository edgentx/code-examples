package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// THE DELIVERY CONTRACT. Everything else in this file exists to serve it.
//
// A subscriber route answers 200 in all three cases below. What decides the
// message's fate is the BODY, and this is the single most misread rule in the
// whole building block:
//
//	200 + empty body         -> acknowledged. The message is done; never redelivered.
//	200 + {"status":"RETRY"} -> redeliver. The broker hands it back later.
//	200 + {"status":"DROP"}  -> stop delivering this message to this consumer.
//
// A non-2xx status is also a retry, so a handler that panics into a 500 gets
// redelivery by accident rather than by decision. The failure that costs an
// agency money is the opposite one: a handler that swallows an error and falls
// out of the function returning a bare 200, which acknowledges a message it
// never processed. That message is gone, and nothing anywhere records that it
// existed. Every path below therefore ends in an explicit ack, retry, or drop.
//
// One correction to the folklore, verified against daprd 1.15.5 and captured in
// the README: DROP does not mean "discard" when the subscription declares a
// deadLetterTopic. The sidecar logs the drop and forwards the message to the
// dead-letter topic instead of throwing it away. That is the behavior this
// example wants -- a dropped message still ends up somewhere a human can see
// it -- but it is the opposite of what "DROP" sounds like, so do not rely on
// DROP to make a message disappear.
const (
	// RetryBody is the exact body that asks for redelivery.
	RetryBody = `{"status":"RETRY"}`
	// DropBody is the exact body that discards the message.
	DropBody = `{"status":"DROP"}`
)

// ParkedNotice is what a human sees when a message could not be processed. The
// dead-letter topic is not a graveyard: it is a work queue with an operator on
// the other end, so the record carries the reason and the original payload.
type ParkedNotice struct {
	EventID  string          `json:"eventId"`
	Type     string          `json:"type"`
	Source   string          `json:"source"`
	Reason   string          `json:"reason"`
	Attempts int             `json:"attempts"`
	ParkedAt string          `json:"parkedAt"`
	Data     json.RawMessage `json:"data"`
}

// Subscriber consumes intake notices delivered by the sidecar.
type Subscriber struct {
	Store           Store
	Publisher       Publisher
	DeadLetterTopic string
	// MaxAttempts is the delivery budget. Once it is spent the message is
	// parked rather than retried forever.
	MaxAttempts int
	// Catalog is the set of record series codes this consumer can process.
	Catalog map[string]bool
	Log     *slog.Logger
	Now     func() time.Time
}

// Routes returns the subscriber's HTTP surface. The two event routes are the
// ones named in components/subscription.yaml.
func (s Subscriber) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events/intake", s.HandleDelivery)
	mux.HandleFunc("POST /events/dead-letter", s.HandleDeadLetter)
	mux.HandleFunc("GET /parked/{eventID}", s.handleParkedLookup)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// HandleDelivery is the subscribed route. It reads the envelope, counts the
// delivery, processes the payload, and answers with one of the three verdicts.
func (s Subscriber) HandleDelivery(w http.ResponseWriter, r *http.Request) {
	event, err := decodeEvent(r)
	if err != nil {
		// Retrying will not turn an unparseable body into a parseable one, so
		// redelivery would only burn the budget. Drop it: the sidecar logs the
		// drop, and with a dead-letter topic configured it forwards the message
		// there rather than discarding it.
		s.Log.Warn("dropping unusable envelope", "error", err)
		drop(w)
		return
	}
	if event.Type != TypeIntakeNotice {
		// Not our event: something is routed wrong upstream. Redelivery cannot
		// fix that either, so stop the delivery loop and let the dead-letter
		// topic hold the evidence of the misrouting.
		s.Log.Warn("dropping unexpected event type", "eventId", event.ID, "type", event.Type)
		drop(w)
		return
	}

	ctx := r.Context()
	// At-least-once delivery means this is the second sighting sooner or later.
	// The stable event id is the whole reason that is detectable.
	seen, _, err := s.Store.Get(ctx, "processed::"+event.ID)
	if err != nil {
		s.Log.Error("state unavailable", "eventId", event.ID, "error", err)
		retry(w)
		return
	}
	if seen != nil {
		s.Log.Info("duplicate delivery acknowledged", "eventId", event.ID)
		ack(w)
		return
	}

	attempts, err := s.countDelivery(ctx, event.ID)
	if err != nil {
		// Either a concurrent delivery of this same event won the etag race, or
		// the store is unavailable. In both cases the attempt number is
		// unknown, and acting on an unknown budget is worse than redelivering.
		s.Log.Warn("could not count delivery", "eventId", event.ID, "error", err)
		retry(w)
		return
	}

	if err := s.process(ctx, event); err != nil {
		s.handleFailure(ctx, w, event, attempts, err)
		return
	}

	// The processed marker is what makes the next delivery of this id a no-op.
	// It is written before the acknowledgement, not after: if this write fails
	// the message is handed back rather than acknowledged, because an
	// acknowledged message with no marker would be reprocessed silently the
	// next time the same notice arrives.
	marker, err := json.Marshal(map[string]string{"processedAt": s.now().UTC().Format(time.RFC3339)})
	if err == nil {
		err = s.Store.Save(ctx, "processed::"+event.ID, marker, "")
	}
	if err != nil && !errors.Is(err, ErrETagConflict) {
		s.Log.Error("could not record completion", "eventId", event.ID, "error", err)
		retry(w)
		return
	}

	s.Log.Info("processed", "eventId", event.ID, "attempts", attempts)
	ack(w)
}

// handleFailure applies the delivery budget. The subscriber cannot tell a
// transient failure from a permanent one -- a store that is down for ten
// seconds and a payload that will never parse both surface as "processing
// returned an error" -- so both take the same route: bounded retries, then park.
func (s Subscriber) handleFailure(ctx context.Context, w http.ResponseWriter, event CloudEvent, attempts int, cause error) {
	if attempts < s.MaxAttempts {
		s.Log.Warn("delivery failed, asking for redelivery",
			"eventId", event.ID, "attempt", attempts, "budget", s.MaxAttempts, "error", cause)
		retry(w)
		return
	}

	event.ParkReason = cause.Error()
	if err := s.Publisher.PublishEvent(ctx, s.DeadLetterTopic, event); err != nil {
		// The budget is spent but the message is not parked yet. Acknowledging
		// now would lose it silently, which is the one outcome this example
		// refuses. Ask for redelivery and try to park it again.
		s.Log.Error("could not park message", "eventId", event.ID, "error", err)
		retry(w)
		return
	}
	s.Log.Warn("parked to dead-letter topic",
		"eventId", event.ID, "topic", s.DeadLetterTopic, "attempts", attempts, "reason", cause)
	ack(w)
}

// HandleDeadLetter is the dead-letter route. It records the parked message so
// an operator can find it. Three paths arrive here: this subscriber's own park
// above, the sidecar's `deadLetterTopic` when its retry budget runs out, and
// the sidecar forwarding a message the delivery route dropped.
//
// This route's own subscription declares no dead-letter topic, because there is
// nowhere further to fall. Every failure below therefore retries until the
// record is written.
func (s Subscriber) HandleDeadLetter(w http.ResponseWriter, r *http.Request) {
	event, err := decodeEvent(r)
	if err != nil {
		// The end of the line: an envelope with no id cannot be filed under
		// one. It is logged here and by the sidecar, and goes no further.
		s.Log.Error("unusable envelope on dead-letter topic", "error", err)
		drop(w)
		return
	}
	reason := event.ParkReason
	if reason == "" {
		// Arrived by the sidecar's own dead-letter path -- retries exhausted,
		// or a drop -- which carries no reason attribute of its own.
		reason = "parked by the sidecar: the consumer never acknowledged this message"
	}
	attempts, _, _ := s.Store.Get(r.Context(), "attempt::"+event.ID)

	record := ParkedNotice{
		EventID:  event.ID,
		Type:     event.Type,
		Source:   event.Source,
		Reason:   reason,
		Attempts: decodeAttempts(attempts),
		ParkedAt: s.now().UTC().Format(time.RFC3339),
		Data:     event.Data,
	}
	value, err := json.Marshal(record)
	if err != nil {
		s.Log.Error("could not encode parked record", "eventId", event.ID, "error", err)
		retry(w)
		return
	}
	// Written unconditionally on top of whatever is there: re-parking the same
	// event is not a conflict, it is the same fact again.
	if err := s.Store.Save(r.Context(), "parked::"+event.ID, value, ""); err != nil && !errors.Is(err, ErrETagConflict) {
		s.Log.Error("could not record parked message", "eventId", event.ID, "error", err)
		retry(w)
		return
	}
	s.Log.Warn("message parked for human review", "eventId", event.ID, "reason", reason)
	ack(w)
}

func (s Subscriber) handleParkedLookup(w http.ResponseWriter, r *http.Request) {
	value, _, err := s.Store.Get(r.Context(), "parked::"+r.PathValue("eventID"))
	switch {
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "state store unavailable"})
	case value == nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no parked message with that event id"})
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(value)
	}
}

// process is the work. It is trivial on purpose: this example is about what
// happens around processing, not about the processing itself.
func (s Subscriber) process(_ context.Context, event CloudEvent) error {
	var notice Notice
	if err := json.Unmarshal(event.Data, &notice); err != nil {
		return fmt.Errorf("%w: payload is not an intake notice", ErrBadEnvelope)
	}
	if !s.Catalog[notice.SeriesCode] {
		return fmt.Errorf("%w: %q", ErrUnknownSeries, notice.SeriesCode)
	}
	return nil
}

// countDelivery is the state-store touch: read the counter with its etag,
// write the successor conditionally. Modeling the attempt number in the store
// keyed by the CloudEvent id is honest about what it costs -- one round trip
// per delivery -- and it is the only counter available to an HTTP subscriber
// that does not want to trust a broker-specific redelivery header.
func (s Subscriber) countDelivery(ctx context.Context, eventID string) (int, error) {
	key := "attempt::" + eventID
	current, etag, err := s.Store.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	next := decodeAttempts(current) + 1
	value, err := json.Marshal(map[string]int{"attempts": next})
	if err != nil {
		return 0, err
	}
	// etag is "" on the first delivery, which asks the store to create the key
	// and fail if it already exists. On later deliveries it is the etag just
	// read, which asks the store to fail if anyone wrote in between.
	if err := s.Store.Save(ctx, key, value, etag); err != nil {
		return 0, err
	}
	return next, nil
}

func (s Subscriber) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func decodeAttempts(raw json.RawMessage) int {
	if raw == nil {
		return 0
	}
	var counter struct {
		Attempts int `json:"attempts"`
	}
	if err := json.Unmarshal(raw, &counter); err != nil {
		return 0
	}
	return counter.Attempts
}

func decodeEvent(r *http.Request) (CloudEvent, error) {
	var event CloudEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: %v", ErrBadEnvelope, err)
	}
	return event, event.Validate()
}

// ack, retry, and drop are the only three ways out of a subscribed route.
func ack(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) }

func retry(w http.ResponseWriter) { writeStatus(w, RetryBody) }

func drop(w http.ResponseWriter) { writeStatus(w, DropBody) }

func writeStatus(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
