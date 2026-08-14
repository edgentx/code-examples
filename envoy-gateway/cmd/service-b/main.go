// Command service-b is the second upstream behind the gateway. It answers
// everything the gateway routes to /api/b, and its /slow endpoint sleeps longer
// than the gateway's route timeout so the 504 in the README is a real one.
package main

import (
	"cmp"
	"log"
	"os"
	"time"

	"github.com/edgentx/code-examples/envoy-gateway/internal/echo"
)

// defaultSlowFor is comfortably longer than the one-second route timeout on
// /api/b/slow, including the gateway's retries.
const defaultSlowFor = 5 * time.Second

func main() {
	slowFor, err := slowForFromEnv()
	if err != nil {
		log.Fatalf("service-b: %v", err)
	}

	config := echo.Config{
		Service: "service-b",
		Addr:    cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"),
		SlowFor: slowFor,
	}
	if err := echo.ListenAndServe(config); err != nil {
		log.Fatalf("service-b: %v", err)
	}
}

// slowForFromEnv reads SLOW_FOR as a Go duration, for example "5s". A
// malformed value stops the process rather than silently falling back: a
// timeout demonstration that quietly stopped being slow would look like the
// gateway configuration had changed.
func slowForFromEnv() (time.Duration, error) {
	raw := os.Getenv("SLOW_FOR")
	if raw == "" {
		return defaultSlowFor, nil
	}
	return time.ParseDuration(raw)
}
