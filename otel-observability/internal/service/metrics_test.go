package service_test

import (
	"testing"

	"github.com/edgentx/code-examples/otel-observability/internal/service"
	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collect pulls the front desk's metrics out of the manual reader and returns
// the scope this example's own instruments were registered on. A manual reader
// is the metric equivalent of the in-memory span exporter: no collector, no
// network, no waiting on an export interval.
func collect(t *testing.T, h *harness) metricdata.ScopeMetrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := h.frontDeskMetrics.Collect(t.Context(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		if scope.Scope.Name == service.ScopeName {
			return scope
		}
	}
	t.Fatalf("no metrics recorded on scope %q", service.ScopeName)
	return metricdata.ScopeMetrics{}
}

func metricByName(t *testing.T, scope metricdata.ScopeMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, m := range scope.Metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("metric %q not recorded", name)
	return metricdata.Metrics{}
}

// attrsMatch reports whether a data point carries exactly the route and status
// class expected. "Exactly" matters: an extra attribute is an extra time series.
func attrsMatch(set attribute.Set, route, statusClass string) bool {
	if set.Len() != 2 {
		return false
	}
	gotRoute, hasRoute := set.Value(telemetry.RouteKey)
	gotClass, hasClass := set.Value(telemetry.StatusClassKey)
	return hasRoute && hasClass &&
		gotRoute.AsString() == route && gotClass.AsString() == statusClass
}

// TestRequestMetrics drives a handful of requests through the front desk and
// asserts both instruments recorded them, bucketed by route template and status
// class -- and, just as importantly, that nothing else appeared. Four requests
// across three distinct case files produce two time series, not four.
func TestRequestMetrics(t *testing.T) {
	h := newHarness(t)

	requests := []string{knownCase, slowCase, knownCase, missingCase}
	for _, caseID := range requests {
		h.get(t, caseID, nil)
	}

	scope := collect(t, h)

	t.Run("counter increments per status class", func(t *testing.T) {
		counter, ok := metricByName(t, scope, telemetry.RequestCountName).Data.(metricdata.Sum[int64])
		if !ok {
			t.Fatal("request count is not an int64 sum")
		}

		want := []struct {
			statusClass string
			value       int64
		}{
			{"2xx", 3},
			{"4xx", 1},
		}
		if len(counter.DataPoints) != len(want) {
			t.Fatalf("counter has %d data points, want %d -- an identifier has leaked into the attributes",
				len(counter.DataPoints), len(want))
		}
		for _, expected := range want {
			found := false
			for _, point := range counter.DataPoints {
				if attrsMatch(point.Attributes, service.FrontDeskPath, expected.statusClass) {
					found = true
					if point.Value != expected.value {
						t.Errorf("%s count = %d, want %d", expected.statusClass, point.Value, expected.value)
					}
				}
			}
			if !found {
				t.Errorf("no data point for status class %s on route %s", expected.statusClass, service.FrontDeskPath)
			}
		}
	})

	t.Run("histogram records every request", func(t *testing.T) {
		histogram, ok := metricByName(t, scope, telemetry.RequestDurationName).Data.(metricdata.Histogram[float64])
		if !ok {
			t.Fatal("request duration is not a float64 histogram")
		}

		var total uint64
		for _, point := range histogram.DataPoints {
			if !attrsMatch(point.Attributes, service.FrontDeskPath, "2xx") &&
				!attrsMatch(point.Attributes, service.FrontDeskPath, "4xx") {
				t.Errorf("unexpected histogram attribute set: %v", point.Attributes.ToSlice())
			}
			total += point.Count
		}
		if total != uint64(len(requests)) {
			t.Errorf("histogram recorded %d observations, want %d", total, len(requests))
		}

		// The deliberately slow case file is in the 2xx bucket, so the maximum
		// observation there is the latency an operator would go hunting for.
		slowest, ok := histogramFor(t, histogram, "2xx").Max.Value()
		if !ok || slowest <= 0 {
			t.Errorf("histogram max duration = %v (recorded: %t), want a positive duration", slowest, ok)
		}
	})
}

func histogramFor(t *testing.T, histogram metricdata.Histogram[float64], statusClass string) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, point := range histogram.DataPoints {
		if attrsMatch(point.Attributes, service.FrontDeskPath, statusClass) {
			return point
		}
	}
	t.Fatalf("no histogram data point for status class %s", statusClass)
	return metricdata.HistogramDataPoint[float64]{}
}

// TestStatusClass pins the bucketing that keeps the status attribute to a
// handful of values instead of one per response code.
func TestStatusClass(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{304, "3xx"},
		{404, "4xx"},
		{429, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
	}
	for _, test := range tests {
		if got := telemetry.StatusClass(test.status); got != test.want {
			t.Errorf("StatusClass(%d) = %q, want %q", test.status, got, test.want)
		}
	}
}
