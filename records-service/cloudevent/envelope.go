// Package cloudevent builds and reads the message envelope the services
// exchange: CloudEvents 1.0 in JSON, carrying W3C Trace Context as the
// distributed tracing extension.
//
// Two services agree on an envelope so that neither has to know the other's
// internals. The envelope names the producer (`source`), the kind of fact
// (`type`), the thing the fact is about (`subject`), and gives the fact a
// stable identity (`id`) that a consumer deduplicates on. The tracing extension
// carries `traceparent`, so a message that sits in a queue for a minute still
// lands in the trace the request that produced it started.
//
// The envelope is written by hand rather than pulled from a library because the
// wire format is the interface between the services: the field names and the
// required attributes are the part a reviewer needs to see.
package cloudevent

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SpecVersion is the CloudEvents version this code writes and reads.
const SpecVersion = "1.0"

// ContentTypeJSON is the media type of every payload here.
const ContentTypeJSON = "application/json"

var (
	// ErrMissingAttribute is returned when a required CloudEvents attribute is
	// absent. Required means required: a consumer that accepts an envelope with
	// no id has no deduplication key and therefore no exactly-once behavior.
	ErrMissingAttribute = errors.New("cloudevent is missing a required attribute")
	// ErrUnsupportedSpecVersion is returned for an envelope this code cannot
	// safely read.
	ErrUnsupportedSpecVersion = errors.New("unsupported cloudevents specversion")
)

// Envelope is a CloudEvents 1.0 message in the JSON format. The JSON tags are
// the wire contract; the attribute names are lowercase and unabbreviated because
// the specification says so, not because Go would have chosen them.
type Envelope struct {
	SpecVersion     string    `json:"specversion"`
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	Type            string    `json:"type"`
	Subject         string    `json:"subject,omitempty"`
	Time            time.Time `json:"time"`
	DataContentType string    `json:"datacontenttype"`
	// TraceParent is the distributed tracing extension attribute. The extension
	// deliberately reuses the W3C header name, so the value a consumer reads out
	// of the envelope is the value it would have read off an HTTP request.
	TraceParent string          `json:"traceparent,omitempty"`
	TraceState  string          `json:"tracestate,omitempty"`
	Data        json.RawMessage `json:"data"`
}

// New builds an envelope for one fact. The identifier is supplied rather than
// generated here: it is the consumer's deduplication key, so it has to be
// derived from the fact and stored beside it, not invented at serialization
// time where a retry would produce a different one.
func New(id, source, eventType, subject string, at time.Time, span SpanContext,
	data []byte) Envelope {
	return Envelope{
		SpecVersion:     SpecVersion,
		ID:              id,
		Source:          source,
		Type:            eventType,
		Subject:         subject,
		Time:            at.UTC(),
		DataContentType: ContentTypeJSON,
		TraceParent:     span.TraceParent(),
		Data:            append(json.RawMessage(nil), data...),
	}
}

// Marshal serializes the envelope after checking it is complete.
func (e Envelope) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal cloudevent %s: %w", e.ID, err)
	}
	return encoded, nil
}

// Unmarshal reads an envelope and checks it is complete before returning it, so
// no consumer has to remember to validate.
func Unmarshal(encoded []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("unmarshal cloudevent: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// Validate checks the attributes CloudEvents 1.0 requires.
func (e Envelope) Validate() error {
	if e.SpecVersion != SpecVersion {
		return fmt.Errorf("%w: %q", ErrUnsupportedSpecVersion, e.SpecVersion)
	}
	for _, attribute := range []struct {
		name  string
		value string
	}{
		{"id", e.ID},
		{"source", e.Source},
		{"type", e.Type},
	} {
		if attribute.value == "" {
			return fmt.Errorf("%w: %s", ErrMissingAttribute, attribute.name)
		}
	}
	return nil
}

// Span returns the trace the message belongs to, and whether there was a usable
// one to return. A consumer calls Child on it to record its own work inside the
// same trace.
func (e Envelope) Span() (SpanContext, bool) {
	span, err := ParseTraceParent(e.TraceParent)
	if err != nil {
		return SpanContext{}, false
	}
	return span, true
}
