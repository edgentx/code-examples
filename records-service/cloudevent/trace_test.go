package cloudevent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/edgentx/code-examples/records-service/cloudevent"
)

// A well-formed version 00 header, used as the base for the malformed variants
// so each case differs from a valid header in exactly one way.
const validHeader = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestParseTraceParentAccepts(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		wantTraceID string
		wantSpanID  string
		wantSampled bool
	}{
		{
			name:        "the sampled example from the specification",
			header:      validHeader,
			wantTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			wantSpanID:  "00f067aa0ba902b7",
			wantSampled: true,
		},
		{
			name:        "an unsampled trace is still a trace",
			header:      "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
			wantTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			wantSpanID:  "00f067aa0ba902b7",
			wantSampled: false,
		},
		{
			name:        "unknown flag bits are ignored rather than rejected",
			header:      "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-03",
			wantTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			wantSpanID:  "00f067aa0ba902b7",
			wantSampled: true,
		},
		{
			// Forward compatibility is the reason the version prefix exists. A
			// sender running a later specification must not have its trace
			// dropped by a receiver running this one.
			name:        "a later version with extra fields keeps the first four",
			header:      validHeader + "-something-a-future-version-added",
			wantTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			wantSpanID:  "00f067aa0ba902b7",
			wantSampled: true,
		},
		{
			name:        "surrounding whitespace is tolerated",
			header:      "  " + validHeader + "  ",
			wantTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			wantSpanID:  "00f067aa0ba902b7",
			wantSampled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := test.header
			if strings.HasPrefix(header, validHeader+"-") {
				header = "01" + header[2:]
			}

			span, err := cloudevent.ParseTraceParent(header)

			if err != nil {
				t.Fatalf("ParseTraceParent(%q): %v", header, err)
			}
			if span.TraceIDString() != test.wantTraceID {
				t.Errorf("trace id = %s, want %s", span.TraceIDString(), test.wantTraceID)
			}
			if span.SpanIDString() != test.wantSpanID {
				t.Errorf("span id = %s, want %s", span.SpanIDString(), test.wantSpanID)
			}
			if span.Sampled != test.wantSampled {
				t.Errorf("Sampled = %t, want %t", span.Sampled, test.wantSampled)
			}
		})
	}
}

func TestParseTraceParentRefuses(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantErr error
	}{
		{
			name:    "nothing to continue",
			header:  "",
			wantErr: cloudevent.ErrNoTraceParent,
		},
		{
			name:    "only whitespace",
			header:  "   ",
			wantErr: cloudevent.ErrNoTraceParent,
		},
		{
			name:    "truncated",
			header:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			name:    "the reserved version",
			header:  "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			// An all-zero identifier looks like a trace and correlates nothing,
			// which is worse than having no header at all.
			name:    "an all-zero trace id",
			header:  "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			name:    "an all-zero parent id",
			header:  "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			// The specification requires lowercase. Accepting both spellings
			// would make one trace compare unequal to itself downstream.
			name:    "an uppercase trace id",
			header:  "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			name:    "a non-hexadecimal parent id",
			header:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902bz-01",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			name:    "trailing data on a version 00 header",
			header:  validHeader + "-extra",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
		{
			name:    "a later version whose fourth field runs on",
			header:  "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0100",
			wantErr: cloudevent.ErrMalformedTraceParent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := cloudevent.ParseTraceParent(test.header)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseTraceParent(%q) error = %v, want %v",
					test.header, err, test.wantErr)
			}
		})
	}
}

// TestContinueOrStartAlwaysProducesATrace is the rule every entry point relies
// on: a bad header never leaves a request untraced, and never silently
// reparents it onto a trace that was not readable.
func TestContinueOrStartAlwaysProducesATrace(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantSameAs string
	}{
		{"a readable header is continued", validHeader, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"an absent header starts a trace", "", ""},
		{"an unreadable header starts a trace", "not-a-traceparent-at-all-really", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			span, err := cloudevent.ContinueOrStart(test.header)

			if err != nil {
				t.Fatalf("ContinueOrStart: %v", err)
			}
			if !span.Valid() {
				t.Fatal("ContinueOrStart produced an unusable span context")
			}
			if test.wantSameAs != "" && span.TraceIDString() != test.wantSameAs {
				t.Errorf("trace id = %s, want the inbound %s",
					span.TraceIDString(), test.wantSameAs)
			}
			if test.wantSameAs == "" && span.TraceIDString() == "" {
				t.Error("a new trace has no trace id")
			}
		})
	}
}

// TestChildKeepsTheTraceAndMovesTheSpan is the property the whole correlation
// story rests on. Keeping the span id as well would make two hops
// indistinguishable; changing the trace id would break the correlation the
// header exists for.
func TestChildKeepsTheTraceAndMovesTheSpan(t *testing.T) {
	parent, err := cloudevent.ParseTraceParent(validHeader)
	if err != nil {
		t.Fatalf("ParseTraceParent: %v", err)
	}

	child, err := parent.Child()
	if err != nil {
		t.Fatalf("Child: %v", err)
	}

	if child.TraceIDString() != parent.TraceIDString() {
		t.Errorf("child trace id = %s, want the parent's %s",
			child.TraceIDString(), parent.TraceIDString())
	}
	if child.SpanIDString() == parent.SpanIDString() {
		t.Error("the child reused the parent's span id, so the hops cannot be told apart")
	}
	if child.Sampled != parent.Sampled {
		t.Errorf("child Sampled = %t, want the parent's %t", child.Sampled, parent.Sampled)
	}
}

// TestChildOfNothingStartsATrace covers the zero value, which is what a caller
// holds when it never had a trace to begin with.
func TestChildOfNothingStartsATrace(t *testing.T) {
	child, err := cloudevent.SpanContext{}.Child()

	if err != nil {
		t.Fatalf("Child: %v", err)
	}
	if !child.Valid() {
		t.Fatal("the child of an empty context is unusable")
	}
}

// TestTraceParentRoundTrips proves the writer and the reader agree, which is the
// only thing standing between this service and a partner's tracing tool.
func TestTraceParentRoundTrips(t *testing.T) {
	span, err := cloudevent.StartTrace()
	if err != nil {
		t.Fatalf("StartTrace: %v", err)
	}

	header := span.TraceParent()
	if len(header) != 55 {
		t.Fatalf("traceparent = %q, want 55 characters", header)
	}
	parsed, err := cloudevent.ParseTraceParent(header)
	if err != nil {
		t.Fatalf("ParseTraceParent(%q): %v", header, err)
	}

	if parsed != span {
		t.Errorf("round-tripped span = %+v, want %+v", parsed, span)
	}
}

// TestInvalidContextRendersNothing keeps a zero value from being written onto
// the wire as a real-looking header.
func TestInvalidContextRendersNothing(t *testing.T) {
	if got := (cloudevent.SpanContext{}).TraceParent(); got != "" {
		t.Errorf("TraceParent() = %q, want empty", got)
	}
}
