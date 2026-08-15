// Package outbox turns accepted facts into messages and gets those messages out
// of the store.
//
// The two halves are deliberately separate. Building a message is a pure
// function of the fact, so the same fact always produces the same message id --
// that is what lets a consumer deduplicate a redelivery. Getting messages out is
// a relay that reads rows the writer already committed, publishes them, and
// marks them; it is the only component that talks to the broker, and it can
// crash at any point without losing a fact or inventing one.
//
// Coordination between the two services in this example is event choreography:
// each service reacts to facts and emits its own. There is no coordinator
// process, and no workflow engine is used.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// Source is the CloudEvents `source` attribute for everything this service
// emits. It is a URI-reference identifying the producing context, not a network
// address: consumers route on it, so it must not change with deployment.
const Source = "/agency/records-service"

// Subject is the CloudEvents `subject` for a records request. It names the thing
// the fact is about, so a consumer can filter without parsing the payload.
func Subject(requestID string) string { return "records-request/" + requestID }

// MessageID is the identity of the message announcing one fact. It is derived
// from the fact -- the request and the version the event occupies in its stream
// -- rather than generated, so a message republished after a crash carries the
// identifier it carried the first time and the consumer recognizes it.
func MessageID(requestID string, version int) string {
	return fmt.Sprintf("%s/%d", requestID, version)
}

// Message builds the CloudEvents envelope announcing one committed event.
//
// It is a pure function of the record: the payload is the stored payload, the
// type is the stored event name, the time is the time the fact became true, and
// the trace is the one recorded with the event. Nothing is invented at publish
// time, which is why republishing after a crash produces a byte-identical
// message rather than a second one that merely resembles the first.
func Message(stored recordsrequest.Stored) cloudevent.Envelope {
	// A stored event whose traceparent is missing or unreadable produces an
	// envelope with no traceparent rather than a fresh trace. Inventing one here
	// would attach the consumer's work to an operation that never happened.
	span, _ := cloudevent.ParseTraceParent(stored.Metadata.TraceParent)
	return cloudevent.New(MessageID(stored.RequestID, stored.Version), Source, stored.Name,
		Subject(stored.RequestID), stored.RecordedAt, span, stored.Payload)
}

// Publisher is the driven port for the broker. The relay does not know whether
// the far side is a topic, a queue, or -- as in this example's tests -- another
// service in the same process.
type Publisher interface {
	// Publish hands one message to the broker. It returns an error if the
	// message was not accepted, and the relay leaves the entry undispatched so
	// the next pass tries again.
	Publish(ctx context.Context, envelope cloudevent.Envelope) error
}

// Relay moves committed messages out of the store.
//
// The order of operations is the whole design and it is not interchangeable:
// read, publish, then mark. A relay that marked first would lose every message
// it was holding when the process died. Publishing first means a crash between
// the publish and the mark republishes on restart, which is why the message id
// is stable and why every consumer here deduplicates on it. At-least-once
// delivery plus a deduplicating consumer is exactly-once effect, and it is the
// only combination that survives a machine losing power.
type Relay struct {
	repo      recordsrequest.Repository
	publisher Publisher
	batch     int
	log       *slog.Logger
}

// NewRelay returns a relay that drains at most batch messages per pass.
func NewRelay(repo recordsrequest.Repository, publisher Publisher, batch int,
	log *slog.Logger) *Relay {
	if batch <= 0 {
		batch = 100
	}
	if log == nil {
		log = slog.Default()
	}
	return &Relay{repo: repo, publisher: publisher, batch: batch, log: log}
}

// Drain publishes every pending message it can and returns how many were
// dispatched. A publish failure stops the pass rather than skipping ahead: the
// messages are in the order the facts were decided, and a consumer that sees
// them out of order sees a history that never happened.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	dispatched := 0
	for {
		pending, err := r.repo.PendingOutbox(ctx, r.batch)
		if err != nil {
			return dispatched, fmt.Errorf("read pending outbox: %w", err)
		}
		if len(pending) == 0 {
			return dispatched, nil
		}
		for _, stored := range pending {
			envelope := Message(stored)
			if err := envelope.Validate(); err != nil {
				return dispatched, fmt.Errorf("event %d: %w", stored.Sequence, err)
			}
			if err := r.publisher.Publish(ctx, envelope); err != nil {
				return dispatched, fmt.Errorf("publish %s: %w", envelope.ID, err)
			}
			if err := r.repo.MarkDispatched(ctx, stored.Sequence); err != nil {
				// The message is already out. Failing to mark it means it goes
				// out again on the next pass, which the consumer absorbs.
				return dispatched, fmt.Errorf("mark %s dispatched: %w", envelope.ID, err)
			}
			dispatched++
		}
		if len(pending) < r.batch {
			return dispatched, nil
		}
	}
}

// Run drains on a fixed interval until the context is canceled. A failed pass is
// logged and retried on the next tick rather than ending the relay: the messages
// are durable, so there is nothing to lose by waiting.
func (r *Relay) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
				r.log.Error("outbox pass failed", "error", err)
			}
		}
	}
}
