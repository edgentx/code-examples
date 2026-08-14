package echo_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/edgentx/code-examples/envoy-gateway/internal/echo"
)

func TestNewHandlerRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  echo.Config
		wantErr error
	}{
		{
			name:    "blank service name",
			config:  echo.Config{Service: "   "},
			wantErr: echo.ErrNoServiceName,
		},
		{
			name:    "negative delay",
			config:  echo.Config{Service: "service-b", SlowFor: -time.Second},
			wantErr: echo.ErrNegativeDelay,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := echo.NewHandler(test.config)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if handler != nil {
				t.Fatalf("handler = %v, want nil on rejected config", handler)
			}
		})
	}
}

// The echo reply is the observation instrument for the whole example: the
// header-stripping demonstration is only meaningful if a header that arrives is
// guaranteed to appear in the reply.
func TestEchoReportsWhatItReceived(t *testing.T) {
	handler := mustHandler(t, echo.Config{Service: "service-a"})

	request := httptest.NewRequest(http.MethodGet, "http://gateway.example/records/42", nil)
	request.Header.Set("X-User-Id", "forged-caller")
	request.Header.Add("X-Forwarded-For", "203.0.113.9")
	request.Header.Add("X-Forwarded-For", "198.51.100.4")

	reply := do(t, handler, request)

	if reply.Service != "service-a" {
		t.Errorf("service = %q, want service-a", reply.Service)
	}
	if reply.Path != "/records/42" {
		t.Errorf("path = %q, want /records/42", reply.Path)
	}
	if reply.Host != "gateway.example" {
		t.Errorf("host = %q, want gateway.example", reply.Host)
	}
	if got := reply.Headers["x-user-id"]; got != "forged-caller" {
		t.Errorf("x-user-id = %q, want forged-caller: an arriving header must be visible in the reply", got)
	}
	if got := reply.Headers["x-forwarded-for"]; got != "198.51.100.4, 203.0.113.9" {
		t.Errorf("x-forwarded-for = %q, want both values joined", got)
	}
	if _, present := reply.Headers["X-User-Id"]; present {
		t.Error("header names must be lowercased so assertions match the gateway configuration")
	}
}

// The gateway proves its route timeout by giving up on this endpoint. That only
// works if the endpoint really is slower than the timeout, so the delay is
// asserted rather than assumed.
func TestSlowEndpointOutlastsItsDelay(t *testing.T) {
	const delay = 60 * time.Millisecond
	handler := mustHandler(t, echo.Config{Service: "service-b", SlowFor: delay})

	start := time.Now()
	reply := do(t, handler, httptest.NewRequest(http.MethodGet, "http://gateway.example/slow", nil))
	elapsed := time.Since(start)

	if elapsed < delay {
		t.Errorf("elapsed = %s, want at least %s", elapsed, delay)
	}
	if reply.Path != "/slow" {
		t.Errorf("path = %q, want /slow", reply.Path)
	}
}

// When the gateway's timeout fires it resets the stream, canceling the request
// context. The upstream must stop waiting rather than hold a goroutine for the
// full delay behind a caller that is already gone.
func TestSlowEndpointStopsWhenTheCallerGivesUp(t *testing.T) {
	handler := mustHandler(t, echo.Config{Service: "service-b", SlowFor: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example/slow", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler kept sleeping after the caller canceled")
	}
}

func TestHealthz(t *testing.T) {
	handler := mustHandler(t, echo.Config{Service: "service-a"})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://gateway.example/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != "ok\n" {
		t.Errorf("body = %q, want %q", body, "ok\n")
	}
}

func TestFlattenHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   map[string]string
	}{
		{
			name:   "empty",
			header: http.Header{},
			want:   map[string]string{},
		},
		{
			name:   "canonical casing is lowered",
			header: http.Header{"X-Request-Id": {"c0ffee"}},
			want:   map[string]string{"x-request-id": "c0ffee"},
		},
		{
			name:   "repeated values are joined in a stable order",
			header: http.Header{"X-User-Roles": {"reviewer", "analyst"}},
			want:   map[string]string{"x-user-roles": "analyst, reviewer"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := echo.FlattenHeaders(test.header)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("FlattenHeaders() = %v, want %v", got, test.want)
			}
		})
	}
}

func mustHandler(t *testing.T, config echo.Config) http.Handler {
	t.Helper()
	handler, err := echo.NewHandler(config)
	if err != nil {
		t.Fatalf("NewHandler(%+v): %v", config, err)
	}
	return handler
}

func do(t *testing.T, handler http.Handler, request *http.Request) echo.Reply {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("content-type"); contentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", contentType)
	}

	var reply echo.Reply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decoding reply %q: %v", recorder.Body.String(), err)
	}
	return reply
}
