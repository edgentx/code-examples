// Package choreography wires the two services together.
//
// It is one file because that is the honest size of the coordination in this
// design: three subscriptions and a relay. There is no coordinator, no state
// machine held outside the aggregates, and no engine. Each service reacts to a
// fact and emits its own, and the sequence that results -- release, assemble,
// report, close or compensate -- is a consequence of what each side does on its
// own rather than a script somewhere that says what happens next.
//
// The same wiring runs in the acceptance criteria, in the tests, and in
// cmd/server, which is what keeps "it passed the tests" and "it works when you
// run it" from being separate claims.
package choreography

import (
	"log/slog"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/bus"
	"github.com/edgentx/code-examples/records-service/delivery"
	"github.com/edgentx/code-examples/records-service/fulfillment"
	"github.com/edgentx/code-examples/records-service/outbox"
	"github.com/edgentx/code-examples/records-service/projector"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
	"github.com/edgentx/code-examples/records-service/requests"
)

// FulfillmentServiceID is the identifier the authorization model knows the
// fulfillment service by. It is the only principal permitted to report a
// delivery outcome.
const FulfillmentServiceID = "records-fulfillment"

// Office is one wired deployment: a records service, a fulfillment service, the
// transport between them, and the relay that gets messages out of the store.
type Office struct {
	Repo        recordsrequest.Repository
	Model       recordsrequest.Projection
	Projector   *projector.Projector
	Access      authz.Store
	Requests    *requests.Service
	Fulfillment *fulfillment.Service
	Bus         *bus.Bus
	Relay       *outbox.Relay
}

// Config is what has to be decided from outside.
type Config struct {
	// Repo is the event store, either adapter.
	Repo recordsrequest.Repository
	// Model is the read model the console's list is served from. Both adapters
	// implement it on the same database as the stream.
	Model recordsrequest.Projection
	// Access is the authorization store, either adapter.
	Access authz.Store
	// OfficeID names the office whose requests this deployment handles.
	OfficeID string
	// Assembler builds release packages.
	Assembler fulfillment.Assembler
	// Options adjust the records service, chiefly its clock in tests.
	Options []requests.Option
	// Log receives relay failures. A nil logger uses the default.
	Log *slog.Logger
}

// Wire builds the deployment and registers the three subscriptions that are the
// whole of the coordination:
//
//	records_request.fulfilled  -> the fulfillment service assembles a package
//	records_delivery.confirmed -> the records service closes the request
//	records_delivery.failed    -> the records service compensates the release
func Wire(config Config) *Office {
	messages := bus.New()
	service := requests.New(config.Repo, config.Model, config.Access, config.OfficeID,
		config.Options...)
	packages := fulfillment.New(config.Assembler, messages)
	consumer := requests.NewDeliveryConsumer(service, authz.ServicePrincipal(FulfillmentServiceID))

	messages.Subscribe("records_request.fulfilled", packages.Handle)
	messages.Subscribe(delivery.TypeConfirmed, consumer.Handle)
	messages.Subscribe(delivery.TypeFailed, consumer.Handle)

	return &Office{
		Repo:        config.Repo,
		Model:       config.Model,
		Projector:   projector.New(config.Repo, config.Model, 0, config.Log),
		Access:      config.Access,
		Requests:    service,
		Fulfillment: packages,
		Bus:         messages,
		Relay:       outbox.NewRelay(config.Repo, messages, 50, config.Log),
	}
}

// RelayThrough builds a relay that publishes through something other than the
// bus directly. It exists for the crash cases, which need a publisher that can
// deliver a message and then fail.
func (o *Office) RelayThrough(publisher outbox.Publisher, log *slog.Logger) *outbox.Relay {
	return outbox.NewRelay(o.Repo, publisher, 50, log)
}

// Staff writes the relationship facts for an office: who works there in what
// capacity, and which service delivers its packages. It is the authorization
// state a deployment starts with, and it is short enough to read in full.
func Staff(officeID string, clerks, reviewers []string) []authz.Tuple {
	office := authz.OfficeObject(officeID)
	tuples := make([]authz.Tuple, 0, len(clerks)+len(reviewers)+1)
	for _, clerk := range clerks {
		tuples = append(tuples, authz.Tuple{
			User: authz.UserPrincipal(clerk), Relation: "clerk", Object: office,
			Why: clerk + " works the " + officeID + " intake desk",
		})
	}
	for _, reviewer := range reviewers {
		tuples = append(tuples, authz.Tuple{
			User: authz.UserPrincipal(reviewer), Relation: "reviewer", Object: office,
			Why: reviewer + " is a records officer for " + officeID,
		})
	}
	return append(tuples, authz.Tuple{
		User:     authz.ServicePrincipal(FulfillmentServiceID),
		Relation: "fulfillment",
		Object:   office,
		Why:      "release packages for " + officeID + " are assembled by this service",
	})
}
