package service

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// caseFile is one synthetic record in the records office catalog.
type caseFile struct {
	CaseID    string `json:"case_id"`
	Custodian string `json:"custodian"`
	Status    string `json:"status"`
	Pages     int    `json:"pages"`

	// shelfDelay stands in for a slow storage tier. One case file is
	// deliberately slow so the README demonstration has something to find: the
	// citizen reports a slow request, and the trace shows the time is spent in
	// the archive read inside the records service, not at the front desk.
	shelfDelay time.Duration
}

// caseFiles is the whole catalog. All of it is invented.
var caseFiles = map[string]caseFile{
	"CASE-2026-0142": {CaseID: "CASE-2026-0142", Custodian: "Records Division", Status: "released", Pages: 12},
	"CASE-2026-0187": {CaseID: "CASE-2026-0187", Custodian: "Permitting Office", Status: "under review", Pages: 3},
	"CASE-2026-0203": {CaseID: "CASE-2026-0203", Custodian: "Off-site Archive", Status: "released", Pages: 418,
		shelfDelay: 250 * time.Millisecond},
}

// NewRecords builds the records office service: one route that returns a case
// file, or 404 when no such file exists.
func NewRecords(deps Deps) (http.Handler, error) {
	instruments, err := telemetry.NewInstruments(deps.Meter, ScopeName)
	if err != nil {
		return nil, err
	}
	tracer := deps.Tracer.Tracer(ScopeName)

	mux := http.NewServeMux()
	mount(mux, deps, instruments, http.MethodGet, RecordsPath, func(w http.ResponseWriter, r *http.Request) {
		caseID := r.PathValue("caseID")

		// A hand-written child span around the work that actually costs time.
		// Automatic instrumentation can only tell you the handler took 260ms;
		// this span is what tells you 250ms of it was the archive read.
		ctx, span := tracer.Start(r.Context(), ArchiveSpanName, trace.WithAttributes(
			attribute.String("agency.case_id", caseID),
			attribute.String("agency.shelf", "off-site"),
		))
		defer span.End()

		file, found := caseFiles[caseID]
		span.SetAttributes(attribute.Bool("agency.case_found", found))
		if !found {
			deps.Logger.WarnContext(ctx, "case file not in catalog", slog.String("case_id", caseID))
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such case file"})
			return
		}

		span.SetAttributes(attribute.String("agency.custodian", file.Custodian))
		if file.shelfDelay > 0 {
			time.Sleep(file.shelfDelay)
		}

		deps.Logger.InfoContext(ctx, "case file read",
			slog.String("case_id", caseID),
			slog.Int("pages", file.Pages))
		writeJSON(w, http.StatusOK, file)
	})

	return mux, nil
}
