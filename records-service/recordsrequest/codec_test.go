package recordsrequest_test

import (
	"errors"
	"testing"

	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// TestEventCodecRoundTrips is the storage contract in one table. Every event has
// to survive being written and read back exactly, because the written form is
// the record: a field that does not round-trip is a fact the agency will not be
// able to produce later.
func TestEventCodecRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		event recordsrequest.Event
	}{
		{"submitted", recordsrequest.Submitted{
			RequestID:   "PRR-2026-0041",
			Requester:   "M. Alvarez",
			Description: "Inspection reports for the Fifth Street bridge, 2025",
			At:          day0,
			DueAt:       day0.AddDate(0, 0, 10),
		}},
		{"acknowledged", recordsrequest.Acknowledged{RequestID: "PRR-2026-0041", At: day1}},
		{"reviewer assigned", recordsrequest.ReviewerAssigned{
			RequestID: "PRR-2026-0041", Reviewer: "records.officer.7", At: day1,
		}},
		{"fulfilled", recordsrequest.Fulfilled{
			RequestID: "PRR-2026-0041", ReleasedPages: 18, At: day2,
		}},
		{"delivery confirmed", recordsrequest.DeliveryConfirmed{
			RequestID: "PRR-2026-0041", PackageID: "PKG-2026-0188", At: day3,
		}},
		{"release failed", recordsrequest.ReleaseFailed{
			RequestID: "PRR-2026-0041", Reason: "two pages are still under legal hold", At: day3,
		}},
		{"denied", recordsrequest.Denied{
			RequestID: "PRR-2026-0041", Exemption: "personnel file exemption", At: day2,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := recordsrequest.EncodeEvent(test.event)
			if err != nil {
				t.Fatalf("EncodeEvent: %v", err)
			}

			decoded, err := recordsrequest.DecodeEvent(test.event.EventName(), payload)
			if err != nil {
				t.Fatalf("DecodeEvent: %v", err)
			}

			if decoded != test.event {
				t.Errorf("round-tripped event = %+v, want %+v", decoded, test.event)
			}
			if decoded.EventName() != test.event.EventName() {
				t.Errorf("name = %q, want %q", decoded.EventName(), test.event.EventName())
			}
			if !decoded.OccurredAt().Equal(test.event.OccurredAt()) {
				t.Errorf("occurred at %s, want %s", decoded.OccurredAt(), test.event.OccurredAt())
			}
		})
	}
}

// TestDecodeRejectsUnknownNames is the same rule replay follows, one layer down.
// A stream carrying a fact this code does not understand must fail loudly rather
// than decode into nothing.
func TestDecodeRejectsUnknownNames(t *testing.T) {
	_, err := recordsrequest.DecodeEvent("records_request.transferred", []byte(`{}`))

	if !errors.Is(err, recordsrequest.ErrUnknownEvent) {
		t.Fatalf("DecodeEvent error = %v, want %v", err, recordsrequest.ErrUnknownEvent)
	}
}

// TestDecodeRejectsCorruptPayloads keeps a damaged row from loading as a
// zero-valued fact, which would look like a request that was submitted by
// nobody, for nothing, at the zero time.
func TestDecodeRejectsCorruptPayloads(t *testing.T) {
	_, err := recordsrequest.DecodeEvent("records_request.submitted", []byte(`not json`))

	if err == nil {
		t.Fatal("a corrupt payload decoded without an error")
	}
}

// TestStreamOfEncodedEventsReplays proves the codec and the aggregate agree: a
// history written to bytes and read back rebuilds the same request.
func TestStreamOfEncodedEventsReplays(t *testing.T) {
	original := closed()

	rebuilt := make([]recordsrequest.Event, 0, len(original))
	for _, event := range original {
		payload, err := recordsrequest.EncodeEvent(event)
		if err != nil {
			t.Fatalf("EncodeEvent(%s): %v", event.EventName(), err)
		}
		decoded, err := recordsrequest.DecodeEvent(event.EventName(), payload)
		if err != nil {
			t.Fatalf("DecodeEvent(%s): %v", event.EventName(), err)
		}
		rebuilt = append(rebuilt, decoded)
	}

	fromEvents, err := recordsrequest.FromHistory(original)
	if err != nil {
		t.Fatalf("FromHistory: %v", err)
	}
	fromBytes, err := recordsrequest.FromHistory(rebuilt)
	if err != nil {
		t.Fatalf("FromHistory after the round trip: %v", err)
	}

	if recordsrequest.SummaryOf(fromEvents) != recordsrequest.SummaryOf(fromBytes) {
		t.Errorf("stored history rebuilt %+v, want %+v",
			recordsrequest.SummaryOf(fromBytes), recordsrequest.SummaryOf(fromEvents))
	}
}
