package intake

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Notice is the synthetic agency payload: a records office telling the rest of
// the estate that a batch of documents arrived and was booked in.
type Notice struct {
	NoticeID   string `json:"noticeId"`
	AgencyCode string `json:"agencyCode"`
	SeriesCode string `json:"seriesCode"`
	PageCount  int    `json:"pageCount"`
}

// Validate rejects a notice the estate cannot act on. Validation happens before
// the publish, on purpose: a broker is not a place to discover that a field was
// blank, because by then the bad message has fanned out to every subscriber.
func (n Notice) Validate() error {
	switch {
	case n.NoticeID == "", n.AgencyCode == "", n.SeriesCode == "":
		return ErrMissingField
	case n.PageCount <= 0:
		return ErrNotPositive
	}
	return nil
}

// IntakeAPI is the publishing service. It validates and publishes. That is the
// whole job: no fan-out logic, no downstream calls, no state of its own.
type IntakeAPI struct {
	Publisher Publisher
	Topic     string
	Log       *slog.Logger
	// Now is injected so tests get a fixed `time` attribute.
	Now func() time.Time
}

// Routes returns the publishing service's HTTP surface.
func (a IntakeAPI) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /intake", a.handleIntake)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (a IntakeAPI) handleIntake(w http.ResponseWriter, r *http.Request) {
	var notice Notice
	if err := json.NewDecoder(r.Body).Decode(&notice); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed intake notice"})
		return
	}
	if err := notice.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	data, err := json.Marshal(notice)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encode notice"})
		return
	}
	// The event id is the notice id, not a fresh UUID. Publish this notice
	// twice -- a client retry, a redeployed pod replaying its queue -- and the
	// subscriber sees one id twice and can tell it is the same fact.
	event := NewCloudEvent(TypeIntakeNotice, SourcePublisher, notice.NoticeID, a.now(), data)
	event.TraceParent = r.Header.Get("traceparent")

	if err := a.Publisher.PublishEvent(r.Context(), a.Topic, event); err != nil {
		a.Log.Error("publish failed", "eventId", event.ID, "topic", a.Topic, "error", err)
		// The caller is told the publish failed so it can retry with the same
		// notice id. Answering 202 here would be the quiet drop.
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not publish intake notice"})
		return
	}

	a.Log.Info("published", "eventId", event.ID, "type", event.Type, "topic", a.Topic)
	writeJSON(w, http.StatusAccepted, map[string]string{"eventId": event.ID, "topic": a.Topic})
}

func (a IntakeAPI) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
