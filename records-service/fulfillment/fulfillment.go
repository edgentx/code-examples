// Package fulfillment is the second service: it assembles and delivers release
// packages, and reports what happened.
//
// It is a separate service in the sense that matters. It shares no database with
// the records service, calls none of its functions, and knows nothing about
// aggregates, versions or outboxes. It consumes one fact and emits one fact.
// That is the whole coupling, and it is why the transaction that spans the two
// is coordinated by choreography -- each side reacts to facts and emits its own
// -- rather than by a coordinator telling both what to do. No workflow engine is
// involved.
//
// The compensation is the interesting half. Assembling a package can fail
// permanently: a record turns out to still be under legal hold, and no number of
// retries will change that. There is no way to reach back into the records
// service and unwind its transaction, so this service reports the failure as a
// fact and the records service compensates its own release. The two states that
// result -- delivered, or released-then-withdrawn -- are both states the record
// can defend.
package fulfillment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/delivery"
	"github.com/edgentx/code-examples/records-service/outbox"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// Assembler builds and dispatches the package of released records. It is a port:
// in a deployment it talks to a document repository and a mail or portal
// service; in this example it is a small fake the tests steer.
type Assembler interface {
	// Assemble returns the identifier of the delivered package. A Refusal means
	// the package cannot be delivered and never will be, so the release must be
	// compensated. Any other error is treated as temporary and the message is
	// left for redelivery.
	Assemble(ctx context.Context, requestID string, pages int) (string, error)
}

// Refusal is a permanent failure. Distinguishing it from an ordinary error is
// the decision the whole compensation path turns on: retrying a permanent
// failure forever is how a request ends up stuck in release_pending with nobody
// looking at it.
type Refusal struct {
	Reason string
}

func (r Refusal) Error() string { return "package cannot be delivered: " + r.Reason }

// Service consumes release facts and reports delivery outcomes.
type Service struct {
	assembler Assembler
	publisher outbox.Publisher

	mu       sync.Mutex
	handled  map[string]struct{}
	assembly int
}

// New wires the service to its assembler and to the transport it reports on.
func New(assembler Assembler, publisher outbox.Publisher) *Service {
	return &Service{
		assembler: assembler,
		publisher: publisher,
		handled:   make(map[string]struct{}),
	}
}

// Handle processes one released-records message.
//
// The deduplication is on the CloudEvents id, and it is checked first. Delivery
// is at least once by construction -- the records service's relay publishes
// before it marks, so a crash in between republishes -- and a records office
// that mails the same package twice because of a relay restart has done real
// harm. This is the consumer half of exactly-once: the transport promises the
// message arrives, the consumer promises it takes effect once.
func (s *Service) Handle(ctx context.Context, envelope cloudevent.Envelope) error {
	if s.alreadyHandled(envelope.ID) {
		return nil
	}

	event, err := recordsrequest.DecodeEvent(envelope.Type, envelope.Data)
	if err != nil {
		return err
	}
	released, ok := event.(recordsrequest.Fulfilled)
	if !ok {
		return fmt.Errorf("fulfillment received %s, which it does not handle", envelope.Type)
	}

	// The trace continues here rather than starting again. The operator who
	// released the records and the package this service assembled are the same
	// operation, so an incident search on either lands on both.
	span, found := envelope.Span()
	if !found {
		return fmt.Errorf("release message %s carries no readable trace context", envelope.ID)
	}
	work, err := span.Child()
	if err != nil {
		return err
	}

	outcome := delivery.Outcome{RequestID: released.RequestID}
	eventType := delivery.TypeConfirmed

	packageID, err := s.assemble(ctx, released.RequestID, released.ReleasedPages)
	var refusal Refusal
	switch {
	case errors.As(err, &refusal):
		eventType = delivery.TypeFailed
		outcome.Reason = refusal.Reason
	case err != nil:
		// Temporary. Nothing is recorded as handled, so the redelivery this
		// error asks for will do the work.
		return fmt.Errorf("assembling the package for %s: %w", released.RequestID, err)
	default:
		outcome.PackageID = packageID
	}

	payload, err := json.Marshal(outcome)
	if err != nil {
		return fmt.Errorf("encoding the delivery outcome: %w", err)
	}
	report := cloudevent.New(delivery.MessageID(envelope.ID), delivery.Source, eventType,
		envelope.Subject, envelope.Time, work, payload)
	if err := s.publisher.Publish(ctx, report); err != nil {
		return fmt.Errorf("reporting the delivery outcome: %w", err)
	}

	s.markHandled(envelope.ID)
	return nil
}

// Assemblies is how many packages were actually assembled, which is what a test
// asserts when it wants to know that a redelivery had no second effect.
func (s *Service) Assemblies() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.assembly
}

// assemble counts the attempts that reached the assembler.
func (s *Service) assemble(ctx context.Context, requestID string, pages int) (string, error) {
	s.mu.Lock()
	s.assembly++
	s.mu.Unlock()

	return s.assembler.Assemble(ctx, requestID, pages)
}

// alreadyHandled reports whether this exact message has been processed.
func (s *Service) alreadyHandled(messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, found := s.handled[messageID]
	return found
}

// markHandled records a message as processed, after the outcome has been
// reported. Recording it earlier would drop the work if the report failed.
func (s *Service) markHandled(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handled[messageID] = struct{}{}
}
