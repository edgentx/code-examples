package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edgentx/code-examples/otel-observability/internal/telemetry"
)

// version is reported as the service version resource attribute.
const version = "0.1.0"

// shutdownGrace bounds both the HTTP drain and the telemetry flush.
const shutdownGrace = 5 * time.Second

// Run is the process entry point shared by both binaries: set up telemetry,
// build the handler, serve until a signal arrives, then drain and flush.
//
// The flush is not decoration. Whatever is still sitting in the batch span
// processor when the process exits is lost, and the spans most worth keeping are
// usually the ones from the minute before a restart.
func Run(serviceName, addr string, build func(Deps) (http.Handler, error)) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := telemetry.NewLogger(os.Stdout, serviceName)

	providers, err := telemetry.Setup(ctx, serviceName, version)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := providers.Shutdown(flushCtx); err != nil {
			logger.Error("telemetry shutdown", slog.Any("error", err))
		}
	}()

	handler, err := build(Deps{Logger: logger, Tracer: providers.Tracer, Meter: providers.Meter})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: shutdownGrace,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", addr))
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return server.Shutdown(drainCtx)
	}
}

// Env returns the environment variable, or fallback when it is unset or empty.
func Env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
