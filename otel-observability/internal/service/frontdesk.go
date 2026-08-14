package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// NewFrontDesk builds the front desk service: it takes the citizen's request
// and fetches the case file from the records office over HTTP.
//
// The outbound client's transport is wrapped by otelhttp, which is the whole of
// the propagation story. The wrapped transport injects the current span into a
// W3C `traceparent` request header; the records office reads it back off the
// inbound request and starts its span as a child of ours. One request, one
// trace, spans from both services.
func NewFrontDesk(deps Deps, recordsBaseURL string) (http.Handler, error) {
	instruments, err := telemetry.NewInstruments(deps.Meter, ScopeName)
	if err != nil {
		return nil, err
	}
	tracer := deps.Tracer.Tracer(ScopeName)
	baseURL := strings.TrimSuffix(recordsBaseURL, "/")

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport,
			otelhttp.WithTracerProvider(deps.Tracer),
			// Name the client span for the route template of the call, not for
			// the resolved URL, so an aggregation over span names stays finite.
			otelhttp.WithSpanNameFormatter(func(string, *http.Request) string {
				return http.MethodGet + " " + RecordsPath
			}),
		),
	}

	mux := http.NewServeMux()
	mount(mux, deps, instruments, http.MethodGet, FrontDeskPath, func(w http.ResponseWriter, r *http.Request) {
		caseID := r.PathValue("caseID")

		// A hand-written span covering the whole downstream interaction. It
		// carries the case identifier: high-cardinality values belong on spans,
		// where you search for one of them, and never on metric attributes,
		// where every distinct value costs a permanent time series.
		ctx, span := tracer.Start(r.Context(), LookupSpanName, trace.WithAttributes(
			attribute.String("agency.case_id", caseID),
			attribute.String("agency.upstream", "records"),
		))
		defer span.End()

		deps.Logger.InfoContext(ctx, "case file requested", slog.String("case_id", caseID))

		target := baseURL + "/records/" + url.PathEscape(caseID)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			fail(ctx, w, span, deps.Logger, http.StatusInternalServerError, "malformed records URL", err)
			return
		}

		response, err := client.Do(request)
		if err != nil {
			fail(ctx, w, span, deps.Logger, http.StatusBadGateway, "records office unreachable", err)
			return
		}
		defer func() { _ = response.Body.Close() }()

		body, err := io.ReadAll(response.Body)
		if err != nil {
			fail(ctx, w, span, deps.Logger, http.StatusBadGateway, "records response truncated", err)
			return
		}

		span.SetAttributes(attribute.Int("agency.records_status", response.StatusCode))
		if response.StatusCode != http.StatusOK {
			// Mark the span an error so the trace backend can surface it
			// without anyone having to read the attributes.
			span.SetStatus(codes.Error, "records office returned "+response.Status)
			deps.Logger.WarnContext(ctx, "case file unavailable",
				slog.String("case_id", caseID),
				slog.Int("records_status", response.StatusCode))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	})

	return mux, nil
}

// fail records the error on the span, logs it with the trace correlation
// already attached, and answers the citizen. Recording on the span is what makes
// the failure findable later by trace id rather than only by log grep.
func fail(ctx context.Context, w http.ResponseWriter, span trace.Span, logger *slog.Logger, status int, message string, cause error) {
	span.RecordError(cause)
	span.SetStatus(codes.Error, message)
	logger.ErrorContext(ctx, message, slog.Any("error", cause))
	writeJSON(w, status, map[string]string{"error": message})
}
