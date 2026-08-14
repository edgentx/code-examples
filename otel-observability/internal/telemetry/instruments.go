package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric instrument names. Keeping them as constants means the dashboard query
// and the code that emits the series cannot drift apart silently.
//
// They are namespaced under `agency.` rather than reusing the semantic
// convention `http.server.*` names because otelhttp already emits those from
// its own instrumentation scope. Two instruments with one name and different
// definitions is a conflict a collector will complain about at export time.
const (
	RequestCountName    = "agency.request.count"
	RequestDurationName = "agency.request.duration"
)

// Attribute keys recorded on every measurement.
const (
	// RouteKey is the route *template*, never the resolved path. `/records/{caseID}`
	// is one series; `/records/CASE-2026-0142` is one series per case file.
	RouteKey = attribute.Key("http.route")
	// StatusClassKey buckets the response code to 2xx/4xx/5xx.
	StatusClassKey = attribute.Key("http.status_class")
)

// Instruments is the metric side of the instrumentation: how many requests, and
// how long they took, sliced by a deliberately tiny attribute set.
//
// Cardinality discipline is the failure everyone hits. A time series exists for
// every distinct combination of attribute values, and the backend pays for each
// one forever. Two routes and three status classes is at most six series per
// service. Add a case identifier, a citizen name, or a raw URL path and the
// series count becomes the request count -- the metrics pipeline falls over and
// the queries stop returning.
//
// The identifiers do not disappear; they move to the span, where high
// cardinality is the point. That is the division of labor: metrics tell you
// that the `/requests/{caseID}` route got slow, traces tell you which case file
// and which downstream hop made it slow.
type Instruments struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

// NewInstruments creates the counter and histogram on a meter named for the
// caller's package path, the conventional instrumentation scope.
func NewInstruments(provider metric.MeterProvider, scope string) (*Instruments, error) {
	meter := provider.Meter(scope)

	requests, err := meter.Int64Counter(RequestCountName,
		metric.WithDescription("Inbound HTTP requests handled."),
		metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", RequestCountName, err)
	}

	duration, err := meter.Float64Histogram(RequestDurationName,
		metric.WithDescription("Inbound HTTP request duration."),
		metric.WithUnit("s"),
		// The SDK's default bucket boundaries are tuned for milliseconds. This
		// histogram is in seconds, so without explicit boundaries every normal
		// request lands in the first bucket and the percentile is meaningless.
		// Latency buckets are part of the instrument definition, not a
		// dashboard setting.
		metric.WithExplicitBucketBoundaries(
			0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", RequestDurationName, err)
	}

	return &Instruments{requests: requests, duration: duration}, nil
}

// Observe records one completed request. Both instruments carry the same
// attribute set so a rate and a latency percentile can be joined on the same
// labels in a dashboard.
func (i *Instruments) Observe(ctx context.Context, route string, statusCode int, elapsed time.Duration) {
	attrs := metric.WithAttributes(
		RouteKey.String(route),
		StatusClassKey.String(StatusClass(statusCode)),
	)
	i.requests.Add(ctx, 1, attrs)
	i.duration.Record(ctx, elapsed.Seconds(), attrs)
}

// StatusClass collapses a response code into the handful of buckets an operator
// actually alerts on.
func StatusClass(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "5xx"
	case statusCode >= 400:
		return "4xx"
	case statusCode >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}
