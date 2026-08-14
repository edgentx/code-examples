// Command publisher is the intake API: it validates a document intake notice
// and publishes it to a topic. That is the whole service.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	intake "github.com/edgentx/code-examples/dapr-pubsub"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	api := intake.IntakeAPI{
		Publisher: intake.SidecarPublisher{
			BaseURL:   env("DAPR_HTTP_ENDPOINT", "http://127.0.0.1:3500"),
			Component: env("PUBSUB_COMPONENT", "intake-pubsub"),
			Client:    &http.Client{Timeout: 5 * time.Second},
		},
		Topic: env("INTAKE_TOPIC", "intake-notices"),
		Log:   log,
	}

	addr := env("LISTEN_ADDR", ":8080")
	log.Info("intake api listening", "addr", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           api.Routes(),
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
