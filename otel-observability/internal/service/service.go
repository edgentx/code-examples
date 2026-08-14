// Package service holds the two synthetic agency services that make up the call
// chain: a front desk that takes the citizen's request, and a records office it
// calls to fetch the case file. Both are ordinary net/http handlers; all the
// observability is in the wrapping.
package service

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// ScopeName is the instrumentation scope reported on every span and metric this
// package emits. Convention is the import path of the instrumenting code, which
// tells whoever reads the trace which library produced the span.
const ScopeName = "github.com/edgentx/code-examples/otel-observability"

// Route templates. These strings are the span names and the `http.route`
// metric attribute, so they must stay templates: the moment a resolved case
// identifier gets in here the metric cardinality is unbounded.
const (
	FrontDeskPath = "/requests/{caseID}"
	RecordsPath   = "/records/{caseID}"
)

// Span names for the hand-written spans, exported so the tests assert on the
// same constants the handlers emit.
const (
	LookupSpanName  = "records.lookup"
	ArchiveSpanName = "archive.read"
)

// Deps is everything a handler needs from the outside. The providers are passed
// in rather than read from the OpenTelemetry globals so a test can run both
// services in one process with one provider each and still tell their spans
// apart by service name.
type Deps struct {
	Logger *slog.Logger
	Tracer trace.TracerProvider
	Meter  metric.MeterProvider
}

// mount registers one route with automatic server-side tracing and the request
// metrics. The span name is the route template rather than the request path,
// for the same reason the metric attribute is.
func mount(mux *http.ServeMux, deps Deps, instruments *telemetry.Instruments, method, path string, handler http.HandlerFunc) {
	pattern := method + " " + path
	mux.Handle(pattern, otelhttp.NewHandler(
		measure(instruments, path, handler),
		pattern,
		otelhttp.WithTracerProvider(deps.Tracer),
		otelhttp.WithMeterProvider(deps.Meter),
	))
}

// measure records the counter and the histogram once the handler has returned.
// It sits inside the tracing wrapper so the measurement context already carries
// the server span, which is what lets an exemplar link a slow latency bucket
// back to the trace that landed in it.
func measure(instruments *telemetry.Instruments, route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(recorder, r)
		instruments.Observe(r.Context(), route, recorder.status, time.Since(started))
	}
}

// statusRecorder remembers the response code so it can be bucketed into a
// status class. A handler that never calls WriteHeader has written a 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
