package service_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/edgentx/code-examples/otel-observability/internal/service"
	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
)

// Synthetic case identifiers from the records catalog.
const (
	knownCase   = "CASE-2026-0142"
	slowCase    = "CASE-2026-0203"
	missingCase = "CASE-2026-9999"
)

// harness runs both services in one process, each with its own tracer provider
// so their spans are distinguishable by service name, both writing to one
// in-memory exporter. The records office is a real HTTP server on loopback:
// propagation has to survive an actual request, not a function call.
type harness struct {
	spans            *tracetest.InMemoryExporter
	frontDesk        http.Handler
	frontDeskMetrics *sdkmetric.ManualReader
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	telemetry.UseW3CPropagation()
	spans := tracetest.NewInMemoryExporter()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	tracerFor := func(serviceName string) *sdktrace.TracerProvider {
		provider := sdktrace.NewTracerProvider(
			// A synchronous processor: every ended span is in the exporter by
			// the time the request returns, so the assertions need no waiting.
			sdktrace.WithSyncer(spans),
			sdktrace.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL, semconv.ServiceName(serviceName))),
		)
		t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
		return provider
	}

	recordsReader := sdkmetric.NewManualReader()
	recordsHandler, err := service.NewRecords(service.Deps{
		Logger: logger,
		Tracer: tracerFor("records"),
		Meter:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(recordsReader)),
	})
	if err != nil {
		t.Fatalf("build records service: %v", err)
	}
	recordsServer := httptest.NewServer(recordsHandler)
	t.Cleanup(recordsServer.Close)

	frontDeskReader := sdkmetric.NewManualReader()
	frontDesk, err := service.NewFrontDesk(service.Deps{
		Logger: logger,
		Tracer: tracerFor("frontdesk"),
		Meter:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(frontDeskReader)),
	}, recordsServer.URL)
	if err != nil {
		t.Fatalf("build front desk service: %v", err)
	}

	return &harness{spans: spans, frontDesk: frontDesk, frontDeskMetrics: frontDeskReader}
}

// get issues one request to the front desk, optionally carrying inbound headers.
func (h *harness) get(t *testing.T, caseID string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/requests/"+caseID, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	h.frontDesk.ServeHTTP(recorder, request)
	return recorder
}

// find returns the single span with the given name and kind.
func find(t *testing.T, stubs tracetest.SpanStubs, name string, kind trace.SpanKind) tracetest.SpanStub {
	t.Helper()
	var matches []tracetest.SpanStub
	for _, stub := range stubs {
		if stub.Name == name && stub.SpanKind == kind {
			matches = append(matches, stub)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one %q %s span, got %d (all spans: %s)", name, kind, len(matches), names(stubs))
	}
	return matches[0]
}

func names(stubs tracetest.SpanStubs) string {
	out := ""
	for _, stub := range stubs {
		out += "[" + stub.SpanKind.String() + " " + stub.Name + "]"
	}
	return out
}

func serviceName(t *testing.T, stub tracetest.SpanStub) string {
	t.Helper()
	for _, attr := range stub.Resource.Attributes() {
		if attr.Key == semconv.ServiceNameKey {
			return attr.Value.AsString()
		}
	}
	t.Fatalf("span %q has no service.name resource attribute", stub.Name)
	return ""
}

// TestOneRequestIsOneTrace is the claim the example exists to make: a single
// inbound request produces one trace whose spans span both services, linked
// parent to child across the network hop.
func TestOneRequestIsOneTrace(t *testing.T) {
	h := newHarness(t)

	if got := h.get(t, slowCase, nil).Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}

	stubs := h.spans.GetSpans()
	if len(stubs) != 5 {
		t.Fatalf("want 5 spans, got %d: %s", len(stubs), names(stubs))
	}

	frontDeskServer := find(t, stubs, http.MethodGet+" "+service.FrontDeskPath, trace.SpanKindServer)
	lookup := find(t, stubs, service.LookupSpanName, trace.SpanKindInternal)
	recordsClient := find(t, stubs, http.MethodGet+" "+service.RecordsPath, trace.SpanKindClient)
	recordsServer := find(t, stubs, http.MethodGet+" "+service.RecordsPath, trace.SpanKindServer)
	archive := find(t, stubs, service.ArchiveSpanName, trace.SpanKindInternal)

	traceID := frontDeskServer.SpanContext.TraceID()
	for _, stub := range stubs {
		if stub.SpanContext.TraceID() != traceID {
			t.Errorf("span %q trace id = %s, want %s", stub.Name, stub.SpanContext.TraceID(), traceID)
		}
	}

	links := []struct {
		name   string
		child  tracetest.SpanStub
		parent tracetest.SpanStub
	}{
		{"lookup under front desk server", lookup, frontDeskServer},
		{"records client under lookup", recordsClient, lookup},
		{"records server under records client", recordsServer, recordsClient},
		{"archive read under records server", archive, recordsServer},
	}
	for _, link := range links {
		if link.child.Parent.SpanID() != link.parent.SpanContext.SpanID() {
			t.Errorf("%s: parent = %s, want %s", link.name,
				link.child.Parent.SpanID(), link.parent.SpanContext.SpanID())
		}
	}

	if frontDeskServer.Parent.IsValid() {
		t.Errorf("front desk server span has parent %s, want root", frontDeskServer.Parent.SpanID())
	}
	// The records office learned its parent from the wire, not from memory.
	if !recordsServer.Parent.IsRemote() {
		t.Error("records server span parent is not marked remote; the hop was not propagated")
	}

	if got := serviceName(t, frontDeskServer); got != "frontdesk" {
		t.Errorf("front desk service.name = %q", got)
	}
	if got := serviceName(t, recordsServer); got != "records" {
		t.Errorf("records service.name = %q", got)
	}
}

// TestSpanAttributes checks that the hand-written spans carry the identifiers an
// operator searches on. These are the high-cardinality values that belong on a
// span and nowhere near a metric attribute.
func TestSpanAttributes(t *testing.T) {
	h := newHarness(t)
	h.get(t, knownCase, nil)
	stubs := h.spans.GetSpans()

	tests := []struct {
		name  string
		span  string
		kind  trace.SpanKind
		attrs []attribute.KeyValue
	}{
		{
			name: "front desk lookup names the case and the upstream",
			span: service.LookupSpanName,
			kind: trace.SpanKindInternal,
			attrs: []attribute.KeyValue{
				attribute.String("agency.case_id", knownCase),
				attribute.String("agency.upstream", "records"),
				attribute.Int("agency.records_status", http.StatusOK),
			},
		},
		{
			name: "archive read names the case, the shelf, and the outcome",
			span: service.ArchiveSpanName,
			kind: trace.SpanKindInternal,
			attrs: []attribute.KeyValue{
				attribute.String("agency.case_id", knownCase),
				attribute.String("agency.shelf", "off-site"),
				attribute.Bool("agency.case_found", true),
				attribute.String("agency.custodian", "Records Division"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := find(t, stubs, test.span, test.kind)
			for _, want := range test.attrs {
				found := false
				for _, got := range stub.Attributes {
					if got.Key == want.Key && got.Value == want.Value {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("span %q missing attribute %s=%v", test.span, want.Key, want.Value.Emit())
				}
			}
		})
	}
}

// TestInboundTraceparentIsAdopted proves extraction as well as emission: when
// the caller already has a trace, this service continues it instead of starting
// a new one. Without this the gateway's trace and the service's trace are two
// unrelated traces and the citizen's request cannot be followed end to end.
func TestInboundTraceparentIsAdopted(t *testing.T) {
	h := newHarness(t)

	upstreamTrace, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	upstreamSpan, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	traceparent := "00-" + upstreamTrace.String() + "-" + upstreamSpan.String() + "-01"

	h.get(t, knownCase, map[string]string{"traceparent": traceparent})

	stubs := h.spans.GetSpans()
	if len(stubs) == 0 {
		t.Fatal("no spans recorded")
	}
	for _, stub := range stubs {
		if stub.SpanContext.TraceID() != upstreamTrace {
			t.Errorf("span %q trace id = %s, want the caller's %s",
				stub.Name, stub.SpanContext.TraceID(), upstreamTrace)
		}
	}

	frontDeskServer := find(t, stubs, http.MethodGet+" "+service.FrontDeskPath, trace.SpanKindServer)
	if frontDeskServer.Parent.SpanID() != upstreamSpan {
		t.Errorf("parent span id = %s, want %s", frontDeskServer.Parent.SpanID(), upstreamSpan)
	}
	if !frontDeskServer.Parent.IsRemote() {
		t.Error("parent is not marked remote, so it was not read off the wire")
	}
}

// TestMissingCaseIsRecordedAsAnError covers the failed-request half of the ask.
func TestMissingCaseIsRecordedAsAnError(t *testing.T) {
	h := newHarness(t)

	if got := h.get(t, missingCase, nil).Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", got, http.StatusNotFound)
	}

	stubs := h.spans.GetSpans()
	lookup := find(t, stubs, service.LookupSpanName, trace.SpanKindInternal)
	if lookup.Status.Code != codes.Error {
		t.Errorf("lookup span status = %v, want error", lookup.Status.Code)
	}
	archive := find(t, stubs, service.ArchiveSpanName, trace.SpanKindInternal)
	for _, attr := range archive.Attributes {
		if attr.Key == "agency.case_found" && attr.Value.AsBool() {
			t.Error("archive span reports the missing case as found")
		}
	}
}
