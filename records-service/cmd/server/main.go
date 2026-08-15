// Command server runs the records service, the fulfillment service, the outbox
// relay, and the projection in one process, and serves the console beside the
// API.
//
// One process is a demonstration decision, not an architectural one. The two
// services share no data and talk only through messages, so separating them is
// a deployment change; running them together is what lets a reviewer see the
// whole choreography by opening one page.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/choreography"
	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/fulfillment"
	"github.com/edgentx/code-examples/records-service/httpapi"
	"github.com/edgentx/code-examples/records-service/memorystore"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
	"github.com/edgentx/code-examples/records-service/requests"
	"github.com/edgentx/code-examples/records-service/sqlitestore"
	"github.com/edgentx/code-examples/records-service/storetest"
)

// officeID is the office this deployment serves.
const officeID = "midtown"

// The synthetic staff the console is operated as. Identity reaches the service
// as a header that an authorization sidecar stamps in a deployment; there is no
// login here and no credential anywhere in this example.
const (
	clerk    = "c.hall"
	reviewer = "r.okafor"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8081", "address to listen on")
	database := flag.String("db", "", "SQLite file to store events in; empty keeps them in memory")
	web := flag.String("web", "", "directory holding the built console; empty serves the API alone")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*address, *database, *web, log); err != nil {
		log.Error("the service stopped", "error", err)
		os.Exit(1)
	}
}

func run(address, database, web string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := openStore(ctx, database)
	if err != nil {
		return err
	}

	office := choreography.Wire(choreography.Config{
		Repo:      store,
		Model:     store,
		Access:    authz.NewMemory(),
		OfficeID:  officeID,
		Assembler: &courier{},
		Log:       log,
	})
	if err := office.Access.Write(ctx,
		choreography.Staff(officeID, []string{clerk}, []string{reviewer})); err != nil {
		return fmt.Errorf("recording who works in the office: %w", err)
	}
	if err := seed(ctx, office); err != nil {
		return fmt.Errorf("seeding synthetic requests: %w", err)
	}

	// The relay and the projection are the two background consumers of the
	// stream. Both are catch-up loops over durable state, so either can stop and
	// restart without losing anything.
	go office.Relay.Run(ctx, 200*time.Millisecond)
	go office.Projector.Run(ctx, 200*time.Millisecond)

	handler := httpapi.New(office.Requests, nil).Handler()
	if web != "" {
		// The console is served from the same origin as the API, which is why
		// nothing in this example needs a cross-origin policy.
		handler = httpapi.New(office.Requests, os.DirFS(web)).Handler()
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Error("shutdown", "error", err)
		}
	}()

	log.Info("listening", "address", address, "console", web != "", "events", storeKind(database))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openStore returns the event store the deployment was asked for. Both adapters
// pass the same contract, so which one runs is a deployment choice rather than a
// behavioral one.
func openStore(ctx context.Context, database string) (storetest.Store, error) {
	if database == "" {
		return memorystore.New(), nil
	}
	store, err := sqlitestore.Open(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("opening the event store: %w", err)
	}
	return store, nil
}

func storeKind(database string) string {
	if database == "" {
		return "memory"
	}
	return database
}

// seed files a few synthetic requests so the console has something to show. The
// names and the records described are invented for this example.
func seed(ctx context.Context, office *choreography.Office) error {
	filings := []struct {
		requester   string
		description string
		acknowledge bool
		reviewer    string
	}{
		{"M. Alvarez", "Inspection reports for the Fifth Street bridge, 2025", true, "records.officer.7"},
		{"T. Okonkwo", "Correspondence about the Harbor Road speed study", true, ""},
		{"Riverbend Gazette", "Overtime totals for the streets division, fiscal 2025", false, ""},
		{"L. Petrov", "Permit file for 1400 Canal Street", true, "records.officer.9"},
	}

	for i, filing := range filings {
		span, err := cloudevent.StartTrace()
		if err != nil {
			return err
		}
		command := func(step string) requests.Command {
			return requests.Command{
				Principal:       authz.UserPrincipal(clerk),
				IdempotencyKey:  fmt.Sprintf("seed-%d-%s", i, step),
				ExpectedVersion: requests.AnyVersion,
				Trace:           span,
			}
		}

		view, err := office.Requests.Submit(ctx, command("submit"), requests.Submission{
			Requester:   filing.requester,
			Description: filing.description,
		})
		if err != nil {
			return err
		}
		if !filing.acknowledge {
			continue
		}
		if _, err := office.Requests.Acknowledge(ctx, command("acknowledge"), view.ID); err != nil {
			return err
		}
		if filing.reviewer == "" {
			continue
		}
		assign := command("assign")
		assign.Principal = authz.UserPrincipal(reviewer)
		if _, err := office.Requests.AssignReviewer(ctx, assign, view.ID, filing.reviewer); err != nil {
			return err
		}
	}

	if _, err := office.Projector.CatchUp(ctx); err != nil {
		return err
	}
	_, err := office.Relay.Drain(ctx)
	return err
}

// courier stands in for the document repository and the delivery channel. It
// refuses any release of more than 200 pages, which is enough to show the
// compensation path from the console without a switch to flip.
type courier struct{}

func (c *courier) Assemble(_ context.Context, requestID string, pages int) (string, error) {
	if pages > 200 {
		return "", fulfillment.Refusal{
			Reason: "the release exceeds the courier's single-package limit of 200 pages",
		}
	}
	return "PKG-" + requestID, nil
}

// The compiler checks here that the store this command opens really does satisfy
// both halves of the port, rather than finding out at the first request.
var (
	_ recordsrequest.Repository = (*memorystore.Store)(nil)
	_ recordsrequest.Projection = (*memorystore.Store)(nil)
	_ recordsrequest.Repository = (*sqlitestore.Store)(nil)
	_ recordsrequest.Projection = (*sqlitestore.Store)(nil)
)
