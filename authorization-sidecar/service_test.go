package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// call drives one request through a freshly seeded router and decodes the
// envelope. Each case gets its own store so a delete in one case cannot change
// the outcome of another.
func call(t *testing.T, method, target string, headers map[string]string) (*http.Response, envelope) {
	t.Helper()

	request := httptest.NewRequest(method, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	newRouter(newDocumentStore()).ServeHTTP(recorder, request)

	result := recorder.Result()
	var body envelope
	if result.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(result.Body).Decode(&body); err != nil {
			t.Fatalf("decode response body: %v", err)
		}
	}
	return result, body
}

// A sidecar-stamped reader and owner, as Envoy would present them.
var (
	stampedReader = map[string]string{
		headerSubject:  "reader-rosalind",
		headerRoles:    "reader",
		headerDecision: "allow",
	}
	stampedOwner = map[string]string{
		headerSubject:  "owner-omar",
		headerRoles:    "owner,reader",
		headerDecision: "allow",
	}
)

func TestRoutes(t *testing.T) {
	cases := []struct {
		name         string
		method       string
		target       string
		headers      map[string]string
		wantStatus   int
		wantStateTag string
		wantDocs     int
		wantDocID    string
	}{
		{
			name:         "health needs no identity",
			method:       http.MethodGet,
			target:       "/health",
			wantStatus:   http.StatusOK,
			wantStateTag: "ok",
		},
		{
			name:         "readiness needs no identity",
			method:       http.MethodGet,
			target:       "/ready",
			wantStatus:   http.StatusOK,
			wantStateTag: "ok",
		},
		{
			name:       "list returns every seeded document",
			method:     http.MethodGet,
			target:     "/api/documents",
			headers:    stampedReader,
			wantStatus: http.StatusOK,
			wantDocs:   3,
		},
		{
			name:       "fetch returns the requested document",
			method:     http.MethodGet,
			target:     "/api/documents/doc-1002",
			headers:    stampedReader,
			wantStatus: http.StatusOK,
			wantDocID:  "doc-1002",
		},
		{
			name:         "fetch of an unknown identifier is not found",
			method:       http.MethodGet,
			target:       "/api/documents/doc-9999",
			headers:      stampedReader,
			wantStatus:   http.StatusNotFound,
			wantStateTag: "not_found",
		},
		{
			name:         "delete removes the document",
			method:       http.MethodDelete,
			target:       "/api/documents/doc-1001",
			headers:      stampedOwner,
			wantStatus:   http.StatusOK,
			wantStateTag: "deleted",
		},
		{
			name:         "delete of an unknown identifier is not found",
			method:       http.MethodDelete,
			target:       "/api/documents/doc-9999",
			headers:      stampedOwner,
			wantStatus:   http.StatusNotFound,
			wantStateTag: "not_found",
		},
		{
			// The service has no route here. It answers 404, not 403: deciding
			// that a caller may not reach a path is the sidecar's job, and the
			// policy denies this path before the request is ever forwarded.
			name:       "a path outside the routing table is not found",
			method:     http.MethodGet,
			target:     "/api/documents/doc-1001/audit-trail",
			headers:    stampedOwner,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "an unsupported method on a known path is rejected by routing",
			method:     http.MethodPost,
			target:     "/api/documents",
			headers:    stampedOwner,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response, body := call(t, testCase.method, testCase.target, testCase.headers)

			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.wantStatus)
			}
			if testCase.wantStateTag != "" && body.Status != testCase.wantStateTag {
				t.Errorf("status field = %q, want %q", body.Status, testCase.wantStateTag)
			}
			if testCase.wantDocs != 0 && len(body.Documents) != testCase.wantDocs {
				t.Errorf("documents = %d, want %d", len(body.Documents), testCase.wantDocs)
			}
			if testCase.wantDocID != "" {
				if body.Document == nil {
					t.Fatalf("document = nil, want %q", testCase.wantDocID)
				}
				if body.Document.ID != testCase.wantDocID {
					t.Errorf("document id = %q, want %q", body.Document.ID, testCase.wantDocID)
				}
			}
		})
	}
}

// TestEchoesStampedIdentity checks the only thing this service does with
// identity: report it back unchanged.
func TestEchoesStampedIdentity(t *testing.T) {
	_, body := call(t, http.MethodGet, "/api/documents", stampedOwner)

	if body.Identity.Subject != "owner-omar" {
		t.Errorf("subject = %q, want %q", body.Identity.Subject, "owner-omar")
	}
	if body.Identity.Roles != "owner,reader" {
		t.Errorf("roles = %q, want %q", body.Identity.Roles, "owner,reader")
	}
	if body.Identity.Decision != "allow" {
		t.Errorf("decision = %q, want %q", body.Identity.Decision, "allow")
	}
}

// TestServesRequestsWithNoIdentityAtAll is the uncomfortable test, and it is
// here deliberately. Reached directly, the service answers a request that
// carries no identity whatsoever, because it has no authorization code to say
// otherwise. That is not a bug in the service; it is the reason the deployment
// must guarantee no route to this port that bypasses the sidecar.
func TestServesRequestsWithNoIdentityAtAll(t *testing.T) {
	response, body := call(t, http.MethodGet, "/api/documents", nil)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body.Identity.Subject != "" {
		t.Errorf("subject = %q, want empty", body.Identity.Subject)
	}
	if len(body.Documents) != 3 {
		t.Errorf("documents = %d, want 3", len(body.Documents))
	}
}

// TestClientSuppliedIdentityHeaderGrantsNothing proves the upstream derives no
// identity from a header a client set itself. The sidecar is what refuses this
// request in the running stack; here we show that even if one arrived, the
// forged value lands in a field the service only echoes.
func TestClientSuppliedIdentityHeaderGrantsNothing(t *testing.T) {
	_, body := call(t, http.MethodGet, "/api/documents", map[string]string{
		headerForgedIdentity: "owner-omar",
		"Authorization":      "Bearer forged",
	})

	if body.Identity.Subject != "" {
		t.Errorf("subject = %q, want empty: a client header must never become a subject", body.Identity.Subject)
	}
	if body.Identity.Roles != "" {
		t.Errorf("roles = %q, want empty", body.Identity.Roles)
	}
	if body.Identity.ClientSuppliedUserID != "owner-omar" {
		t.Errorf("echoed client header = %q, want %q", body.Identity.ClientSuppliedUserID, "owner-omar")
	}
}

// TestDeleteIsObservable confirms the delete actually mutates the store, so the
// owner-only route in the policy is guarding something real.
func TestDeleteIsObservable(t *testing.T) {
	store := newDocumentStore()
	router := newRouter(store)

	deleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/api/documents/doc-1003", nil))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", deleteRecorder.Code, http.StatusOK)
	}

	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/documents/doc-1003", nil))
	if getRecorder.Code != http.StatusNotFound {
		t.Fatalf("fetch after delete = %d, want %d", getRecorder.Code, http.StatusNotFound)
	}
}
