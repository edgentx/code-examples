// Package telemetry wires the OpenTelemetry SDK for a service: a tracer
// provider, a meter provider, the W3C trace-context propagator, and a slog
// handler that stamps the active trace onto every log line.
//
// Nothing here is service specific. Both binaries in this example call Setup
// with their own service name, which is what makes a single trace readable as
// "front desk called records" rather than as four anonymous spans.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// metricInterval is how often the SDK pushes metrics to the collector. Metrics
// are aggregated in process and exported on this interval, so the export cost is
// independent of request volume.
const metricInterval = 10 * time.Second

// Providers holds the SDK objects a service hands to its instrumentation. They
// are passed explicitly rather than read from the OpenTelemetry globals so the
// tests can stand up two independent providers, one per service, in one
// process.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *metric.MeterProvider
}

// UseW3CPropagation installs the propagator that carries a trace across a
// process boundary. TraceContext is the W3C `traceparent` header; Baggage
// carries user-defined key/value pairs alongside it.
//
// The propagator is a process-wide global on purpose: every client and server
// in the process has to agree on the wire format, or the hop silently starts a
// new trace instead of continuing the caller's.
func UseW3CPropagation() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// Setup builds providers that export OTLP over HTTP to the collector named by
// OTEL_EXPORTER_OTLP_ENDPOINT (default http://localhost:4318), installs the W3C
// propagator, and returns the providers for injection into handlers.
func Setup(ctx context.Context, serviceName, serviceVersion string) (*Providers, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}

	providers := &Providers{
		// A batch processor, not a simple one: spans are queued and shipped in
		// batches so an export never sits on the request path.
		Tracer: sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(traceExporter),
		),
		Meter: metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(metric.NewPeriodicReader(metricExporter,
				metric.WithInterval(metricInterval))),
		),
	}

	UseW3CPropagation()
	return providers, nil
}

// Shutdown flushes anything still buffered. Skipping this is the usual reason
// the last few seconds before a crash are missing from the trace backend.
func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.Tracer.Shutdown(ctx), p.Meter.Shutdown(ctx))
}
