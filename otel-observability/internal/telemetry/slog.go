package telemetry

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Log field names for the correlation keys. These exact names are what a log
// search is pivoted on, so they are constants rather than string literals
// scattered through handlers.
const (
	TraceIDField = "trace_id"
	SpanIDField  = "span_id"
)

// NewLogger returns a JSON logger whose every record carries the trace and span
// identifiers of whatever span is active on the context it is called with.
//
// This is the join between the three signals. A citizen reports a slow request
// at 10:14; the log line for that request carries a trace id; the trace id opens
// the whole call chain across both services. Without it, logs and traces are two
// piles of data with no way to line them up.
func NewLogger(out io.Writer, serviceName string) *slog.Logger {
	base := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(&traceHandler{inner: base}).With(slog.String("service", serviceName))
}

// traceHandler decorates another slog.Handler with trace correlation.
type traceHandler struct {
	inner slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle pulls the identifiers off the span context. A record logged outside a
// span is emitted unchanged rather than stamped with zeroed identifiers, which
// would be worse than no field at all: an all-zero trace id looks like a real
// value to a log search.
func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		record.AddAttrs(
			slog.String(TraceIDField, spanContext.TraceID().String()),
			slog.String(SpanIDField, spanContext.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs and WithGroup must rewrap. Returning the inner handler directly --
// which is what embedding a slog.Handler gets you for free -- silently drops the
// correlation the first time anyone calls logger.With(...).
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name)}
}
