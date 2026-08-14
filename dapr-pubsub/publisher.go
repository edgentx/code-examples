package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Publisher is the port the intake API depends on. It is deliberately two
// methods wide: everything the handler needs, nothing about HTTP or sidecars.
// A test substitutes a recording fake and exercises the whole handler with no
// sidecar and no broker running.
type Publisher interface {
	// Publish sends a bare payload. The sidecar wraps it in a CloudEvent
	// envelope and mints `id` and `time` itself. Convenient, and wrong whenever
	// the consumer needs to deduplicate.
	Publish(ctx context.Context, topic string, payload any) error
	// PublishEvent sends an envelope the producer built. The sidecar passes it
	// through unchanged, so `id`, `type`, and `traceparent` are the producer's
	// to control. This is the method the intake handler actually uses.
	PublishEvent(ctx context.Context, topic string, event CloudEvent) error
}

// SidecarPublisher talks to the daprd sidecar over its local HTTP API. It is
// the only file in the example that knows the sidecar's URL shape.
type SidecarPublisher struct {
	// BaseURL is the sidecar's HTTP endpoint, e.g. http://127.0.0.1:3500.
	BaseURL string
	// Component is the pub/sub component name from components/pubsub.yaml.
	Component string
	// Client is the HTTP client to use. A nil client means http.DefaultClient.
	Client *http.Client
}

func (p SidecarPublisher) Publish(ctx context.Context, topic string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	return p.post(ctx, topic, ContentTypeJSON, body, "")
}

func (p SidecarPublisher) PublishEvent(ctx context.Context, topic string, event CloudEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	return p.post(ctx, topic, ContentTypeCloudEvent, body, event.TraceParent)
}

func (p SidecarPublisher) post(ctx context.Context, topic, contentType string, body []byte, traceParent string) error {
	url := fmt.Sprintf("%s/v1.0/publish/%s/%s", p.BaseURL, p.Component, topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build publish request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	// The header goes out as well as the envelope attribute: the sidecar hop is
	// itself a span, and it reads trace context from the header.
	if traceParent != "" {
		req.Header.Set("traceparent", traceParent)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	defer resp.Body.Close()
	// The sidecar answers 204 on success. Treat anything outside 2xx as a
	// rejection the caller has to see -- a publish that quietly failed is the
	// exact hole this example exists to close.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: status %d", ErrPublishRejected, resp.StatusCode)
	}
	return nil
}
