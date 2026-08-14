// Command subscriber consumes intake notices delivered by the sidecar and
// parks the ones it cannot process.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	intake "github.com/edgentx/code-examples/dapr-pubsub"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	sidecar := env("DAPR_HTTP_ENDPOINT", "http://127.0.0.1:3500")
	client := &http.Client{Timeout: 5 * time.Second}

	subscriber := intake.Subscriber{
		Store: intake.SidecarStore{
			BaseURL:   sidecar,
			Component: env("STATE_COMPONENT", "intake-statestore"),
			Client:    client,
		},
		Publisher: intake.SidecarPublisher{
			BaseURL:   sidecar,
			Component: env("PUBSUB_COMPONENT", "intake-pubsub"),
			Client:    client,
		},
		DeadLetterTopic: env("DEAD_LETTER_TOPIC", "intake-notices-parked"),
		MaxAttempts:     envInt("MAX_DELIVERY_ATTEMPTS", 3),
		// The synthetic retention catalog. A notice citing anything else is the
		// poison message this example is built around.
		Catalog: map[string]bool{"RS-100": true, "RS-220": true, "RS-410": true},
		Log:     log,
	}

	addr := env("LISTEN_ADDR", ":8080")
	log.Info("subscriber listening", "addr", addr, "maxAttempts", subscriber.MaxAttempts)
	server := &http.Server{
		Addr:              addr,
		Handler:           subscriber.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if parsed, err := strconv.Atoi(os.Getenv(key)); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}
