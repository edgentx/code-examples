package intake

import (
	"encoding/json"
	"time"
)

// The house event convention. Every message this system puts on a topic is a
// CloudEvent 1.0 envelope, so a consumer can route, deduplicate, and trace a
// message without knowing anything about the payload inside it.
const (
	// SpecVersion pins the CloudEvents version. It is on the wire so a future
	// 1.1 consumer can tell what it is holding.
	SpecVersion = "1.0"
	// TypeIntakeNotice is the event type for a document intake notice. The
	// version suffix is part of the contract: a breaking payload change gets a
	// new type rather than a silent reinterpretation of the old one.
	TypeIntakeNotice = "gov.example.records.intake_notice.v1"
	// SourcePublisher identifies the producing service. Together with ID it
	// makes an event uniquely identifiable across the whole estate.
	SourcePublisher = "/records/intake-api"
	// ContentTypeJSON is what a plain publish sends. The sidecar sees a bare
	// JSON body and wraps it in a CloudEvent envelope for us.
	ContentTypeJSON = "application/json"
	// ContentTypeCloudEvent is what a pre-built publish sends. The sidecar
	// recognizes the envelope and passes it through instead of wrapping it,
	// which is the only way the producer keeps control of `id` and `type`.
	ContentTypeCloudEvent = "application/cloudevents+json"
)

// CloudEvent is the envelope, spelled out rather than pulled from a library so
// the field names on the wire are visible in the example.
//
// `id` deserves the emphasis: idempotency depends on a stable event id. At-
// least-once delivery means a consumer WILL see the same message twice, and the
// only way it can recognize the repeat is if the producer derives `id` from
// something stable about the business fact (here, the notice identifier) rather
// than minting a fresh UUID per publish attempt. A publisher that retries with
// a new id has converted one intake notice into two.
type CloudEvent struct {
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	ID              string          `json:"id"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
	// TraceParent is the W3C distributed-tracing extension attribute. It is
	// carried in the envelope so a trace survives the hop through the broker,
	// where there is no HTTP request to hang a header on. Empty when the
	// inbound request had no trace context, and omitted rather than sent blank.
	TraceParent string `json:"traceparent,omitempty"`
	// ParkReason is a private extension attribute set when this example parks a
	// message on the dead-letter topic. CloudEvents requires extension names to
	// be lowercase alphanumeric, hence `parkreason` and not `parkReason`.
	ParkReason string `json:"parkreason,omitempty"`
}

// NewCloudEvent builds an envelope with the house defaults filled in. The
// caller supplies id explicitly; there is no default, because a defaulted id is
// how producers accidentally ship non-idempotent events.
func NewCloudEvent(eventType, source, id string, at time.Time, data json.RawMessage) CloudEvent {
	return CloudEvent{
		SpecVersion:     SpecVersion,
		Type:            eventType,
		Source:          source,
		ID:              id,
		Time:            at.UTC().Format(time.RFC3339),
		DataContentType: ContentTypeJSON,
		Data:            data,
	}
}

// Validate checks the attributes a router cannot work without. Anything the
// subscriber reads to make a decision has to be present before it decides.
func (e CloudEvent) Validate() error {
	switch {
	case e.SpecVersion == "", e.Type == "", e.Source == "", e.ID == "":
		return ErrBadEnvelope
	}
	return nil
}
