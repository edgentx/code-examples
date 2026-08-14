package intake

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStoreGet covers the read half: value plus etag, and absence that is not
// an error.
func TestStoreGet(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		etag      string
		body      string
		wantValue string
		wantETag  string
		wantErr   error
	}{
		{name: "present", status: http.StatusOK, etag: "7", body: `{"attempts":2}`, wantValue: `{"attempts":2}`, wantETag: "7"},
		{name: "absent returns no error", status: http.StatusNoContent},
		{name: "not found returns no error", status: http.StatusNotFound},
		{name: "store failure surfaces", status: http.StatusInternalServerError, wantErr: ErrStateFailed},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.0/state/intake-statestore/attempt::N-1" {
					t.Errorf("path = %q", r.URL.Path)
				}
				if testCase.etag != "" {
					w.Header().Set("ETag", testCase.etag)
				}
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			store := SidecarStore{BaseURL: server.URL, Component: "intake-statestore"}
			value, etag, err := store.Get(t.Context(), "attempt::N-1")
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Get() error = %v, want %v", err, testCase.wantErr)
			}
			if string(value) != testCase.wantValue {
				t.Errorf("value = %q, want %q", value, testCase.wantValue)
			}
			if etag != testCase.wantETag {
				t.Errorf("etag = %q, want %q", etag, testCase.wantETag)
			}
		})
	}
}

// TestStoreSaveIsConditional pins the request the sidecar receives. The
// concurrency option is the load-bearing part: without first-write the etag is
// accepted and ignored, and a lost update is indistinguishable from a success.
func TestStoreSaveIsConditional(t *testing.T) {
	var sent []stateItem
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/state/intake-statestore" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decode save body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	store := SidecarStore{BaseURL: server.URL, Component: "intake-statestore"}
	if err := store.Save(t.Context(), "attempt::N-1", json.RawMessage(`{"attempts":3}`), "7"); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	if len(sent) != 1 {
		t.Fatalf("sent %d items, want 1", len(sent))
	}
	item := sent[0]
	if item.Key != "attempt::N-1" || string(item.Value) != `{"attempts":3}` || item.ETag != "7" {
		t.Errorf("item = %+v, want the key, value, and etag as read", item)
	}
	if item.Options.Concurrency != "first-write" {
		t.Errorf("concurrency = %q, want first-write", item.Options.Concurrency)
	}
	if item.Options.Consistency != "strong" {
		t.Errorf("consistency = %q, want strong", item.Options.Consistency)
	}
}

// TestStoreSaveConflict is the 409 path. It has its own error so a caller can
// tell "somebody beat me to it" from "the store is broken" -- the first is
// retryable in place, the second is not.
func TestStoreSaveConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	store := SidecarStore{BaseURL: server.URL, Component: "intake-statestore"}
	err := store.Save(t.Context(), "attempt::N-1", json.RawMessage(`{"attempts":3}`), "7")
	if !errors.Is(err, ErrETagConflict) {
		t.Fatalf("Save() = %v, want ErrETagConflict", err)
	}
	if errors.Is(err, ErrStateFailed) {
		t.Error("a lost etag race must not look like a store outage")
	}
}
