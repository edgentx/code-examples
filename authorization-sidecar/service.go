// Package main is the upstream service that sits behind the authorization
// sidecar.
//
// ============================================================================
// THIS SERVICE CONTAINS NO AUTHORIZATION CODE. THAT IS THE ENTIRE POINT.
// ============================================================================
//
// There is no token parsing here, no role check, no allow-list, no "if user is
// admin". Grep the package: the words "role" and "subject" appear only where a
// header stamped by the sidecar is read back out and echoed to the caller so
// the demo is visible.
//
// The service listens on the loopback-facing container port only. Every request
// it can receive has already passed through Envoy, which called Open Policy
// Agent over ext_authz and refused to forward anything the policy did not
// allow. So the contract this service is written against is:
//
//   - It trusts ONLY the identity headers the sidecar stamps: x-authz-subject,
//     x-authz-roles, x-authz-decision. Those headers are set (not appended) by
//     the authorization response, so a value a client tried to smuggle in under
//     the same name is overwritten before the service ever sees the request.
//   - It never reads a client-supplied identity header such as x-user-id, and
//     it never reads the Authorization header. Credentials are the sidecar's
//     problem. This service could not forge or misinterpret a claim if it
//     wanted to, because it never looks at the source of one.
//   - It assumes the deployment guarantees no path to this port that bypasses
//     the sidecar. In the compose stack that is a shared container network; in
//     a cluster it is a pod-local listener plus mutual TLS between proxies.
//
// The payoff for an agency is that an authorization change ships as a policy
// change, reviewable on its own, without recompiling or redeploying any of the
// applications it governs.
package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

// Identity header names. The service treats these as read-only facts produced
// by the sidecar. It writes them nowhere and derives no privilege from them --
// it only echoes them so a curl transcript shows what the policy decided.
const (
	headerSubject  = "X-Authz-Subject"
	headerRoles    = "X-Authz-Roles"
	headerDecision = "X-Authz-Decision"

	// headerForgedIdentity is the header a naive client tries to set itself. It
	// is listed here only so the echo can prove the service ignores it: nothing
	// in this package branches on its value.
	headerForgedIdentity = "X-User-Id"
)

// identity is the sidecar's verdict as the upstream received it.
type identity struct {
	Subject  string `json:"subject"`
	Roles    string `json:"roles"`
	Decision string `json:"decision"`

	// ClientSuppliedUserID is whatever arrived in the X-User-Id header. It is
	// reported so the forged-header demonstration is visible in the response
	// body, and it is used for nothing else, ever.
	ClientSuppliedUserID string `json:"client_supplied_user_id"`
}

func identityFrom(r *http.Request) identity {
	return identity{
		Subject:              r.Header.Get(headerSubject),
		Roles:                r.Header.Get(headerRoles),
		Decision:             r.Header.Get(headerDecision),
		ClientSuppliedUserID: r.Header.Get(headerForgedIdentity),
	}
}

// envelope wraps every response so the identity the sidecar stamped is visible
// next to the data, whatever the endpoint.
type envelope struct {
	Identity  identity   `json:"identity"`
	Status    string     `json:"status,omitempty"`
	Message   string     `json:"message,omitempty"`
	Documents []document `json:"documents,omitempty"`
	Document  *document  `json:"document,omitempty"`
}

// newRouter wires the endpoints. Go 1.22 and newer match the method and path
// wildcards in the pattern itself, so there is no routing logic to review here
// either.
func newRouter(store *documentStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /ready", handleHealth)
	mux.HandleFunc("GET /api/documents", handleListDocuments(store))
	mux.HandleFunc("GET /api/documents/{id}", handleGetDocument(store))
	mux.HandleFunc("DELETE /api/documents/{id}", handleDeleteDocument(store))
	return mux
}

// handleHealth answers the two routes the policy lets through unauthenticated.
// They report liveness and nothing else -- no counts, no identifiers, nothing
// an unauthenticated caller could mine. Keeping the open set this boring is
// what makes it safe to open at all.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, envelope{Identity: identityFrom(r), Status: "ok"})
}

func handleListDocuments(store *documentStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, envelope{
			Identity:  identityFrom(r),
			Documents: store.list(),
		})
	}
}

func handleGetDocument(store *documentStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := store.get(r.PathValue("id"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, envelope{Identity: identityFrom(r), Document: &doc})
	}
}

func handleDeleteDocument(store *documentStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.delete(r.PathValue("id")); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, envelope{
			Identity: identityFrom(r),
			Status:   "deleted",
			Message:  r.PathValue("id"),
		})
	}
}

// writeError maps the store's named errors to status codes. Note what is absent:
// there is no 401 and no 403 branch, because this service never decides one.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNoSuchDocument) {
		writeJSON(w, http.StatusNotFound, envelope{
			Identity: identityFrom(r),
			Status:   "not_found",
			Message:  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, envelope{
		Identity: identityFrom(r),
		Status:   "error",
		Message:  "unexpected failure",
	})
}

func writeJSON(w http.ResponseWriter, status int, body envelope) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(payload, '\n')); err != nil {
		log.Printf("write response: %v", err)
	}
}
