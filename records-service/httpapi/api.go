// Package httpapi is the driving adapter: HTTP in, the application service out.
//
// It holds no rules. Its whole job is to turn a request into a command, name the
// caller, and turn an error into the status code and problem document that tell
// the caller what to do about it. The two that carry weight:
//
//   - 409 with the current version, when the caller edited a request that has
//     since changed. A last-write-wins API would return 200 here and the change
//     the other officer made would be gone with nobody informed. The console
//     turns this into a visible "the record changed" state.
//   - 403 when the authorization model says no. The check happens in the service
//     for every command and every read, so it cannot be skipped by reaching a
//     handler another way.
//
// Identity arrives as a header, stamped by the authorization sidecar in front of
// this service rather than parsed here. That boundary is the subject of
// ../../authorization-sidecar; the point for this example is that the service
// declares the claim it needs and does not implement a login.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
	"github.com/edgentx/code-examples/records-service/requests"
)

// The headers this API reads. They are named once so a test and a handler cannot
// disagree about the spelling.
const (
	// UserHeader carries the authenticated user identifier. In a deployment the
	// sidecar stamps it and the service is unreachable except through the
	// sidecar; the service never derives identity from anything else.
	UserHeader = "X-User-Id"
	// IdempotencyHeader carries the key that makes a retried command harmless.
	IdempotencyHeader = "Idempotency-Key"
	// TraceParentHeader carries the W3C trace to continue.
	TraceParentHeader = "traceparent"
)

// ProblemContentType is the media type of every error this API returns.
const ProblemContentType = "application/problem+json"

// Problem is an RFC 7807 problem document with two additions the console uses.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Code is a stable identifier for the failure, so a caller branches on a
	// value rather than on the wording of Title.
	Code string `json:"code"`
	// CurrentVersion is present on a conflict. It is what turns "your edit was
	// refused" into "your edit was refused, here is where to start again".
	CurrentVersion *int `json:"current_version,omitempty"`
}

// API serves the records service over HTTP.
type API struct {
	service *requests.Service
	// web, when non-nil, is the built console served alongside the API from the
	// same origin, which is why nothing here needs a cross-origin policy.
	web fs.FS
}

// New builds the API. Pass a nil file system to serve the API alone.
func New(service *requests.Service, web fs.FS) *API {
	return &API{service: service, web: web}
}

// Handler returns the router. The patterns are method-qualified, so a GET to a
// command path is a 405 from the router rather than a handler that has to check.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/requests", a.list)
	mux.HandleFunc("POST /api/requests", a.submit)
	mux.HandleFunc("GET /api/requests/{id}", a.get)
	mux.HandleFunc("POST /api/requests/{id}/acknowledgment", a.acknowledge)
	mux.HandleFunc("POST /api/requests/{id}/reviewer", a.assignReviewer)
	mux.HandleFunc("POST /api/requests/{id}/release", a.release)
	mux.HandleFunc("POST /api/requests/{id}/denial", a.deny)

	if a.web != nil {
		mux.Handle("GET /", a.console())
	}
	return mux
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(w, r)
	if !ok {
		return
	}
	views, err := a.service.List(r.Context(), principal)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(w, r)
	if !ok {
		return
	}
	view, err := a.service.View(r.Context(), principal, r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeView(w, http.StatusOK, view)
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Requester          string `json:"requester"`
		RequesterPrincipal string `json:"requester_principal"`
		Description        string `json:"description"`
	}
	cmd, ok := a.command(w, r, &body)
	if !ok {
		return
	}
	view, err := a.service.Submit(r.Context(), cmd, requests.Submission{
		RequesterPrincipal: body.RequesterPrincipal,
		Requester:          body.Requester,
		Description:        body.Description,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeView(w, http.StatusCreated, view)
}

func (a *API) acknowledge(w http.ResponseWriter, r *http.Request) {
	cmd, ok := a.command(w, r, nil)
	if !ok {
		return
	}
	view, err := a.service.Acknowledge(r.Context(), cmd, r.PathValue("id"))
	a.respond(w, r, view, err)
}

func (a *API) assignReviewer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reviewer string `json:"reviewer"`
	}
	cmd, ok := a.command(w, r, &body)
	if !ok {
		return
	}
	view, err := a.service.AssignReviewer(r.Context(), cmd, r.PathValue("id"), body.Reviewer)
	a.respond(w, r, view, err)
}

func (a *API) release(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReleasedPages int `json:"released_pages"`
	}
	cmd, ok := a.command(w, r, &body)
	if !ok {
		return
	}
	view, err := a.service.Release(r.Context(), cmd, r.PathValue("id"), body.ReleasedPages)
	a.respond(w, r, view, err)
}

func (a *API) deny(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Exemption string `json:"exemption"`
	}
	cmd, ok := a.command(w, r, &body)
	if !ok {
		return
	}
	view, err := a.service.Deny(r.Context(), cmd, r.PathValue("id"), body.Exemption)
	a.respond(w, r, view, err)
}

// respond writes the ordinary command result.
func (a *API) respond(w http.ResponseWriter, r *http.Request, view requests.View, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeView(w, http.StatusOK, view)
}

// command reads everything a command needs off the request: who, the
// idempotency key, the version being asserted, the trace to continue, and the
// body if there is one.
func (a *API) command(w http.ResponseWriter, r *http.Request, body any) (requests.Command, bool) {
	principal, ok := a.principal(w, r)
	if !ok {
		return requests.Command{}, false
	}

	key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
	if key == "" {
		writeProblem(w, Problem{
			Status: http.StatusBadRequest,
			Code:   "idempotency_key_required",
			Title:  "The command needs an idempotency key",
			Detail: fmt.Sprintf("Send an %s header so a retry of this command is harmless.",
				IdempotencyHeader),
		})
		return requests.Command{}, false
	}

	expected, ok := expectedVersion(w, r)
	if !ok {
		return requests.Command{}, false
	}

	if body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(body); err != nil {
			writeProblem(w, Problem{
				Status: http.StatusBadRequest,
				Code:   "malformed_body",
				Title:  "The request body could not be read",
				Detail: err.Error(),
			})
			return requests.Command{}, false
		}
	}

	span, err := cloudevent.ContinueOrStart(r.Header.Get(TraceParentHeader))
	if err != nil {
		a.fail(w, r, err)
		return requests.Command{}, false
	}

	return requests.Command{
		Principal:       principal,
		IdempotencyKey:  key,
		ExpectedVersion: expected,
		Trace:           span,
	}, true
}

// principal names the caller, or refuses the request.
func (a *API) principal(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := strings.TrimSpace(r.Header.Get(UserHeader))
	if user == "" {
		writeProblem(w, Problem{
			Status: http.StatusUnauthorized,
			Code:   "unidentified_caller",
			Title:  "The caller is not identified",
			Detail: fmt.Sprintf("The %s header is stamped by the authorization sidecar; "+
				"this service does not accept unauthenticated traffic.", UserHeader),
		})
		return "", false
	}
	return authz.UserPrincipal(user), true
}

// expectedVersion reads the If-Match header. A command with no If-Match asserts
// nothing, which is allowed but is not what the console does: the console always
// sends the version it showed the operator, so an edit made against a screen
// that has gone stale is refused rather than applied.
func expectedVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	if raw == "" {
		return requests.AnyVersion, true
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 0 {
		writeProblem(w, Problem{
			Status: http.StatusBadRequest,
			Code:   "malformed_if_match",
			Title:  "The If-Match header is not a version",
			Detail: fmt.Sprintf("Send the version from the ETag of the request you read, got %q.", raw),
		})
		return 0, false
	}
	return version, true
}

// fail maps an error to the status code and problem document that describe it.
// Every branch names one outcome; there is no default that turns an unexpected
// error into a plausible-looking 400.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, authz.ErrDenied):
		writeProblem(w, Problem{
			Status: http.StatusForbidden,
			Code:   "not_authorized",
			Title:  "The caller may not do this",
			Detail: "The authorization model does not grant this action on this request.",
		})
	case errors.Is(err, recordsrequest.ErrNotFound):
		writeProblem(w, Problem{
			Status: http.StatusNotFound,
			Code:   "not_found",
			Title:  "No such records request",
		})
	case errors.Is(err, recordsrequest.ErrVersionConflict):
		a.conflict(w, r)
	case errors.Is(err, recordsrequest.ErrNoIdempotencyKey):
		writeProblem(w, Problem{
			Status: http.StatusBadRequest,
			Code:   "idempotency_key_required",
			Title:  "The command needs an idempotency key",
		})
	case isRuleViolation(err):
		writeProblem(w, Problem{
			Status: http.StatusUnprocessableEntity,
			Code:   "rule_violated",
			Title:  "The request cannot be changed that way",
			Detail: err.Error(),
		})
	default:
		writeProblem(w, Problem{
			Status: http.StatusInternalServerError,
			Code:   "internal_error",
			Title:  "The command could not be completed",
		})
	}
}

// conflict answers a stale write with the version the caller should start from.
// Reading the request again to find that version is worth the extra query: a
// conflict the operator cannot act on is a dead end.
func (a *API) conflict(w http.ResponseWriter, r *http.Request) {
	problem := Problem{
		Status: http.StatusConflict,
		Code:   "version_conflict",
		Title:  "The record changed while you were working on it",
		Detail: "Review the current version before deciding again; nothing was written.",
	}
	if id := r.PathValue("id"); id != "" {
		if principal := strings.TrimSpace(r.Header.Get(UserHeader)); principal != "" {
			if view, err := a.service.View(r.Context(), authz.UserPrincipal(principal), id); err == nil {
				current := view.Version
				problem.CurrentVersion = &current
			}
		}
	}
	writeProblem(w, problem)
}

// isRuleViolation reports whether the error is the domain refusing a command.
// The list is the domain's own sentinels, so a rule added there without a line
// here surfaces as a 500 and gets noticed, rather than being folded into a 422
// that says nothing.
func isRuleViolation(err error) bool {
	for _, rule := range []error{
		recordsrequest.ErrNotSubmitted,
		recordsrequest.ErrAlreadySubmitted,
		recordsrequest.ErrAlreadyAcknowledged,
		recordsrequest.ErrNotAcknowledged,
		recordsrequest.ErrClosed,
		recordsrequest.ErrReleaseInFlight,
		recordsrequest.ErrNotAwaitingDelivery,
		recordsrequest.ErrNoReviewer,
		recordsrequest.ErrSameReviewer,
		recordsrequest.ErrMissingField,
		recordsrequest.ErrNegativePages,
	} {
		if errors.Is(err, rule) {
			return true
		}
	}
	return false
}

// console serves the built single-page console, falling back to index.html so a
// deep link into the application is served by the application rather than 404.
func (a *API) console() http.Handler {
	files := http.FileServer(http.FS(a.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(a.web, name); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

// writeView writes a rendered request, with the version as the entity tag the
// caller sends back in If-Match.
func writeView(w http.ResponseWriter, status int, view requests.View) {
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(view.Version)))
	writeJSON(w, status, view)
}

// writeJSON writes a JSON body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so there is nothing to tell the
		// caller. Closing the connection is the only signal left.
		panic(http.ErrAbortHandler)
	}
}

// writeProblem writes an RFC 7807 document. The type field is a relative URI so
// the example carries no host name of anybody's.
func writeProblem(w http.ResponseWriter, problem Problem) {
	if problem.Type == "" {
		problem.Type = "/problems/" + problem.Code
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		panic(http.ErrAbortHandler)
	}
}
