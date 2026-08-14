package intake

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// capturedPublish is what the stub sidecar saw.
type capturedPublish struct {
	path        string
	contentType string
	traceParent string
	body        []byte
}

// stubSidecar stands in for daprd. The example never needs a real sidecar to
// prove it speaks the sidecar's HTTP API correctly.
func stubSidecar(t *testing.T, status int) (*httptest.Server, *capturedPublish) {
	t.Helper()
	seen := &capturedPublish{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read publish body: %v", err)
		}
		seen.path = r.URL.Path
		seen.contentType = r.Header.Get("Content-Type")
		seen.traceParent = r.Header.Get("traceparent")
		seen.body = body
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, seen
}

// TestPublishWrapsPayload covers the convenient path: send bare JSON and let
// the sidecar build the envelope.
func TestPublishWrapsPayload(t *testing.T) {
	server, seen := stubSidecar(t, http.StatusNoContent)
	publisher := SidecarPublisher{BaseURL: server.URL, Component: "intake-pubsub"}

	notice := Notice{NoticeID: "N-1001", AgencyCode: "DPR", SeriesCode: "RS-100", PageCount: 4}
	if err := publisher.Publish(t.Context(), "intake-notices", notice); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	if seen.path != "/v1.0/publish/intake-pubsub/intake-notices" {
		t.Errorf("path = %q", seen.path)
	}
	if seen.contentType != ContentTypeJSON {
		t.Errorf("content type = %q, want %q so the sidecar wraps the payload", seen.contentType, ContentTypeJSON)
	}
	// The body is the payload, not an envelope: the id and time do not exist
	// yet, which is exactly why this path cannot support deduplication.
	var raw map[string]any
	if err := json.Unmarshal(seen.body, &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, present := raw["specversion"]; present {
		t.Error("bare publish should not carry an envelope")
	}
}

// TestPublishEventPassesEnvelopeThrough covers the path the intake API uses,
// where the producer owns id and type.
func TestPublishEventPassesEnvelopeThrough(t *testing.T) {
	server, seen := stubSidecar(t, http.StatusNoContent)
	publisher := SidecarPublisher{BaseURL: server.URL, Component: "intake-pubsub"}

	event := NewCloudEvent(TypeIntakeNotice, SourcePublisher, "N-1001",
		time.Date(2026, 3, 4, 8, 30, 0, 0, time.UTC), json.RawMessage(`{"noticeId":"N-1001"}`))
	event.TraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	if err := publisher.PublishEvent(t.Context(), "intake-notices", event); err != nil {
		t.Fatalf("PublishEvent() = %v", err)
	}

	if seen.contentType != ContentTypeCloudEvent {
		t.Errorf("content type = %q, want %q so the sidecar passes the envelope through",
			seen.contentType, ContentTypeCloudEvent)
	}
	if seen.traceParent != event.TraceParent {
		t.Errorf("traceparent header = %q, want it forwarded to the sidecar hop", seen.traceParent)
	}
	var sent CloudEvent
	if err := json.Unmarshal(seen.body, &sent); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if sent.ID != "N-1001" || sent.Type != TypeIntakeNotice {
		t.Errorf("envelope = %+v, want the producer's id and type preserved", sent)
	}
}

func TestPublishFailures(t *testing.T) {
	t.Run("sidecar rejection surfaces", func(t *testing.T) {
		server, _ := stubSidecar(t, http.StatusInternalServerError)
		publisher := SidecarPublisher{BaseURL: server.URL, Component: "intake-pubsub"}
		err := publisher.Publish(t.Context(), "intake-notices", Notice{NoticeID: "N-1"})
		if !errors.Is(err, ErrPublishRejected) {
			t.Errorf("Publish() = %v, want ErrPublishRejected", err)
		}
	})

	t.Run("envelope is validated before it leaves", func(t *testing.T) {
		server, seen := stubSidecar(t, http.StatusNoContent)
		publisher := SidecarPublisher{BaseURL: server.URL, Component: "intake-pubsub"}
		err := publisher.PublishEvent(t.Context(), "intake-notices", CloudEvent{SpecVersion: SpecVersion})
		if !errors.Is(err, ErrBadEnvelope) {
			t.Errorf("PublishEvent() = %v, want ErrBadEnvelope", err)
		}
		if seen.path != "" {
			t.Error("an invalid envelope must not reach the sidecar")
		}
	})
}
