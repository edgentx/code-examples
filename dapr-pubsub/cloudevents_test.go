package intake

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestEnvelopeOnTheWire pins the JSON field names. They are the contract with
// every other language in the estate, so a rename here is a breaking change and
// the test is what makes that visible in a diff.
func TestEnvelopeOnTheWire(t *testing.T) {
	at := time.Date(2026, 3, 4, 8, 30, 0, 0, time.UTC)
	event := NewCloudEvent(TypeIntakeNotice, SourcePublisher, "N-1001", at, json.RawMessage(`{"noticeId":"N-1001"}`))
	event.TraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]string{
		"specversion":     `"1.0"`,
		"type":            `"` + TypeIntakeNotice + `"`,
		"source":          `"` + SourcePublisher + `"`,
		"id":              `"N-1001"`,
		"time":            `"2026-03-04T08:30:00Z"`,
		"datacontenttype": `"application/json"`,
		"data":            `{"noticeId":"N-1001"}`,
		"traceparent":     `"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"`,
	}
	for field, expected := range want {
		got, present := fields[field]
		if !present {
			t.Errorf("envelope is missing %q", field)
			continue
		}
		if string(got) != expected {
			t.Errorf("%s = %s, want %s", field, got, expected)
		}
	}
	// parkreason is only set when a message is parked, and an empty extension
	// attribute must not appear on the wire at all.
	if _, present := fields["parkreason"]; present {
		t.Error("parkreason should be omitted when empty")
	}
}

func TestEnvelopeValidation(t *testing.T) {
	valid := NewCloudEvent(TypeIntakeNotice, SourcePublisher, "N-1", time.Now(), json.RawMessage(`{}`))

	cases := []struct {
		name    string
		mutate  func(*CloudEvent)
		wantErr error
	}{
		{name: "complete envelope", mutate: func(*CloudEvent) {}},
		{name: "no spec version", mutate: func(e *CloudEvent) { e.SpecVersion = "" }, wantErr: ErrBadEnvelope},
		{name: "no type", mutate: func(e *CloudEvent) { e.Type = "" }, wantErr: ErrBadEnvelope},
		{name: "no source", mutate: func(e *CloudEvent) { e.Source = "" }, wantErr: ErrBadEnvelope},
		{name: "no id", mutate: func(e *CloudEvent) { e.ID = "" }, wantErr: ErrBadEnvelope},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := valid
			testCase.mutate(&event)
			if err := event.Validate(); !errors.Is(err, testCase.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}
