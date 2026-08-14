// Package echo is the upstream half of the gateway example: a deliberately
// boring HTTP service that reports exactly what reached it.
//
// The service is uninteresting on purpose. Everything an agency reviewer is
// asked to look at in this example — TLS termination, path routing, identity
// header sanitization, timeouts, retries, access logging — happens in
// envoy-gateway.yaml, at the edge, as reviewable configuration. The upstream
// exists only so the effect of that configuration is observable: whatever the
// gateway forwarded shows up verbatim in the JSON reply, and whatever the
// gateway stripped is missing from it.
package echo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Configuration errors. They are named so a caller can branch on the rule
// rather than matching on message text.
var (
	// ErrNoServiceName is returned when Config.Service is blank. The name is
	// echoed in every reply and is how a reader of the demonstration output
	// tells which upstream answered, so an unnamed service is a broken one.
	ErrNoServiceName = errors.New("echo: service name is required")

	// ErrNegativeDelay is returned when Config.SlowFor is below zero. A
	// negative delay would silently become "no delay", which would make the
	// gateway timeout demonstration quietly stop demonstrating anything.
	ErrNegativeDelay = errors.New("echo: slow-endpoint delay may not be negative")
)

// Config describes one upstream instance.
type Config struct {
	// Service names this upstream in every reply.
	Service string

	// SlowFor is how long the /slow endpoint sleeps before replying. It is set
	// longer than the gateway's route timeout so that the gateway, not the
	// upstream, decides when the caller gives up.
	SlowFor time.Duration

	// Addr is the listen address, host:port.
	Addr string
}

// Reply is the JSON body every endpoint returns.
type Reply struct {
	Service string            `json:"service"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Host    string            `json:"host"`
	Headers map[string]string `json:"headers"`
}

// NewHandler builds the upstream's routes.
//
//   - GET /            echoes the request
//   - GET /slow        sleeps for cfg.SlowFor, then echoes the request
//   - GET /healthz     liveness, no body worth reading
func NewHandler(cfg Config) (http.Handler, error) {
	if strings.TrimSpace(cfg.Service) == "" {
		return nil, ErrNoServiceName
	}
	if cfg.SlowFor < 0 {
		return nil, fmt.Errorf("%w: %s", ErrNegativeDelay, cfg.SlowFor)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// The slow endpoint blocks past the gateway's route timeout. The request
	// context is honored so that when the gateway gives up and resets the
	// stream, this goroutine stops sleeping instead of leaking.
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(cfg.SlowFor):
		case <-r.Context().Done():
			return
		}
		writeReply(w, r, cfg.Service)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeReply(w, r, cfg.Service)
	})

	return mux, nil
}

// ListenAndServe runs an upstream until the server stops.
func ListenAndServe(cfg Config) error {
	handler, err := NewHandler(cfg)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// A bare net/http server will hold a connection open indefinitely while
		// a client dribbles out request headers. The gateway in front of this
		// service is not the only thing that needs a timeout.
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "%s listening on %s\n", cfg.Service, cfg.Addr)
	return server.ListenAndServe()
}

// writeReply serializes what the upstream actually received.
func writeReply(w http.ResponseWriter, r *http.Request, service string) {
	reply := Reply{
		Service: service,
		Method:  r.Method,
		Path:    r.URL.Path,
		// Go strips Host out of the header map, but the authority is what the
		// gateway routed on, so it is reported alongside the rest.
		Host:    r.Host,
		Headers: FlattenHeaders(r.Header),
	}

	w.Header().Set("content-type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(reply); err != nil {
		// The status line is already sent by this point, so there is nothing
		// useful to tell the client. Say it where an operator will see it.
		fmt.Fprintf(os.Stderr, "%s: encoding reply: %v\n", service, err)
	}
}

// FlattenHeaders lowercases header names and joins repeated values, so that the
// echoed JSON is stable enough to assert on. Canonical Go casing
// ("X-User-Id") would make a reader hunt for the header the gateway was
// configured to strip ("x-user-id"); this way the two spellings match.
func FlattenHeaders(header http.Header) map[string]string {
	flat := make(map[string]string, len(header))
	for name, values := range header {
		sorted := append([]string(nil), values...)
		sort.Strings(sorted)
		flat[strings.ToLower(name)] = strings.Join(sorted, ", ")
	}
	return flat
}
