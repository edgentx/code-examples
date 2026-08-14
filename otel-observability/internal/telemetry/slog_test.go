package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestLogRecordsCarryTheActiveTrace is the log-to-trace join the whole example
// depends on: a citizen quotes a timestamp, the log line for it names a trace
// id, and that id opens the full call chain.
func TestLogRecordsCarryTheActiveTrace(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	tests := []struct {
		name       string
		withSpan   bool
		wantFields bool
	}{
		{name: "inside a span the identifiers are stamped on", withSpan: true, wantFields: true},
		{name: "outside a span no identifiers are invented", withSpan: false, wantFields: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			logger := telemetry.NewLogger(&out, "frontdesk").With("route", "/requests/{caseID}")

			ctx := context.Background()
			var wantTrace, wantSpan string
			if test.withSpan {
				var span trace.Span
				ctx, span = provider.Tracer("test").Start(ctx, "records.lookup")
				wantTrace = span.SpanContext().TraceID().String()
				wantSpan = span.SpanContext().SpanID().String()
				defer span.End()
			}

			logger.InfoContext(ctx, "case file requested", "case_id", "CASE-2026-0142")

			var record map[string]any
			if err := json.Unmarshal(out.Bytes(), &record); err != nil {
				t.Fatalf("log line is not JSON: %v (%s)", err, out.String())
			}

			// The With() call above is the regression guard: a handler that
			// forwards WithAttrs to its inner handler loses the correlation.
			if record["route"] != "/requests/{caseID}" {
				t.Errorf("route field = %v, want the value set with With()", record["route"])
			}

			gotTrace, hasTrace := record[telemetry.TraceIDField]
			gotSpan, hasSpan := record[telemetry.SpanIDField]
			if !test.wantFields {
				if hasTrace || hasSpan {
					t.Fatalf("log outside a span carries %s=%v %s=%v",
						telemetry.TraceIDField, gotTrace, telemetry.SpanIDField, gotSpan)
				}
				return
			}
			if gotTrace != wantTrace {
				t.Errorf("%s = %v, want %s", telemetry.TraceIDField, gotTrace, wantTrace)
			}
			if gotSpan != wantSpan {
				t.Errorf("%s = %v, want %s", telemetry.SpanIDField, gotSpan, wantSpan)
			}
		})
	}
}
