package cloudevent_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/edgentx/code-examples/records-service/cloudevent"
)

var occurred = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

// TestEnvelopeWireFormat pins the bytes. The envelope is the interface between
// two services, so the attribute names and their spelling are the contract: a
// consumer written in another language reads these keys and nothing else.
func TestEnvelopeWireFormat(t *testing.T) {
	span, err := cloudevent.ParseTraceParent(validHeader)
	if err != nil {
		t.Fatalf("ParseTraceParent: %v", err)
	}

	encoded, err := cloudevent.New("PRR-2026-0041/4", "/agency/records-service",
		"records_request.fulfilled", "records-request/PRR-2026-0041", occurred, span,
		[]byte(`{"request_id":"PRR-2026-0041","released_pages":18}`)).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("the envelope is not JSON: %v", err)
	}

	want := map[string]string{
		"specversion":     "1.0",
		"id":              "PRR-2026-0041/4",
		"source":          "/agency/records-service",
		"type":            "records_request.fulfilled",
		"subject":         "records-request/PRR-2026-0041",
		"datacontenttype": "application/json",
		"traceparent":     validHeader,
	}
	for attribute, value := range want {
		got, present := wire[attribute]
		if !present {
			t.Errorf("the envelope has no %q attribute", attribute)
			continue
		}
		if got != value {
			t.Errorf("%s = %v, want %q", attribute, got, value)
		}
	}
	if _, present := wire["data"]; !present {
		t.Error("the envelope has no data attribute")
	}
	if _, present := wire["tracestate"]; present {
		t.Error("an empty tracestate was written; optional attributes are omitted, not blank")
	}
}

// TestEnvelopeRoundTrips proves a message survives the transport unchanged,
// including the trace it belongs to.
func TestEnvelopeRoundTrips(t *testing.T) {
	span, err := cloudevent.StartTrace()
	if err != nil {
		t.Fatalf("StartTrace: %v", err)
	}
	payload := []byte(`{"request_id":"PRR-2026-0041"}`)

	encoded, err := cloudevent.New("PRR-2026-0041/1", "/agency/records-service",
		"records_request.submitted", "records-request/PRR-2026-0041", occurred, span,
		payload).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := cloudevent.Unmarshal(encoded)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != "PRR-2026-0041/1" || decoded.Type != "records_request.submitted" {
		t.Errorf("round-tripped envelope = %+v", decoded)
	}
	if !decoded.Time.Equal(occurred) {
		t.Errorf("time = %s, want %s", decoded.Time, occurred)
	}
	if string(decoded.Data) != string(payload) {
		t.Errorf("data = %s, want %s", decoded.Data, payload)
	}
	continued, found := decoded.Span()
	if !found {
		t.Fatal("the round-tripped envelope carries no readable trace context")
	}
	if continued != span {
		t.Errorf("span = %+v, want %+v", continued, span)
	}
}

// TestEnvelopeRefusesIncompleteMessages is the table for the required
// attributes. A consumer that accepts a message with no id has no deduplication
// key, so accepting one would quietly remove the exactly-once property from
// every service downstream.
func TestEnvelopeRefusesIncompleteMessages(t *testing.T) {
	complete := func() cloudevent.Envelope {
		return cloudevent.New("PRR-2026-0041/1", "/agency/records-service",
			"records_request.submitted", "records-request/PRR-2026-0041", occurred,
			cloudevent.SpanContext{}, []byte(`{}`))
	}

	tests := []struct {
		name    string
		break_  func(*cloudevent.Envelope)
		wantErr error
	}{
		{
			name:    "no id, so a consumer cannot deduplicate",
			break_:  func(e *cloudevent.Envelope) { e.ID = "" },
			wantErr: cloudevent.ErrMissingAttribute,
		},
		{
			name:    "no source, so a consumer cannot tell who said it",
			break_:  func(e *cloudevent.Envelope) { e.Source = "" },
			wantErr: cloudevent.ErrMissingAttribute,
		},
		{
			name:    "no type, so a consumer cannot route it",
			break_:  func(e *cloudevent.Envelope) { e.Type = "" },
			wantErr: cloudevent.ErrMissingAttribute,
		},
		{
			name:    "a specversion this code cannot safely read",
			break_:  func(e *cloudevent.Envelope) { e.SpecVersion = "0.3" },
			wantErr: cloudevent.ErrUnsupportedSpecVersion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := complete()
			test.break_(&envelope)

			if err := envelope.Validate(); !errors.Is(err, test.wantErr) {
				t.Errorf("Validate error = %v, want %v", err, test.wantErr)
			}
			if _, err := envelope.Marshal(); !errors.Is(err, test.wantErr) {
				t.Errorf("Marshal error = %v, want %v", err, test.wantErr)
			}

			// The same check has to hold on the way in. A message that should
			// never have been sent must not be accepted merely because it
			// arrived.
			encoded, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("marshaling the broken envelope for the read path: %v", err)
			}
			if _, err := cloudevent.Unmarshal(encoded); !errors.Is(err, test.wantErr) {
				t.Errorf("Unmarshal error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

// TestEnvelopeWithoutATraceIsStillAMessage covers the case where there was no
// trace to record. The message goes out; it simply cannot be correlated.
func TestEnvelopeWithoutATraceIsStillAMessage(t *testing.T) {
	envelope := cloudevent.New("PRR-2026-0041/1", "/agency/records-service",
		"records_request.submitted", "", occurred, cloudevent.SpanContext{}, []byte(`{}`))

	if envelope.TraceParent != "" {
		t.Errorf("traceparent = %q, want empty", envelope.TraceParent)
	}
	if err := envelope.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if _, found := envelope.Span(); found {
		t.Error("a message with no traceparent reported a span")
	}
}
