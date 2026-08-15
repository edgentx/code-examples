package requests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/delivery"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// DeliveryConsumer applies the facts the fulfillment service reports.
//
// It is an inbound adapter and it holds no rules. A confirmation becomes a
// ConfirmDelivery command and a failure becomes a FailDelivery command; the
// aggregate decides what either one means, and the authorization model decides
// whether this consumer's principal is entitled to report it at all. That last
// check is not ceremony: it is the reason a message forged onto the topic cannot
// close somebody's records request.
type DeliveryConsumer struct {
	service   *Service
	principal string
}

// NewDeliveryConsumer wires the consumer to the service, acting as the principal
// the authorization model knows the fulfillment service by.
func NewDeliveryConsumer(service *Service, principal string) *DeliveryConsumer {
	return &DeliveryConsumer{service: service, principal: principal}
}

// Handle applies one delivery outcome.
func (c *DeliveryConsumer) Handle(ctx context.Context, envelope cloudevent.Envelope) error {
	var outcome delivery.Outcome
	if err := json.Unmarshal(envelope.Data, &outcome); err != nil {
		return fmt.Errorf("decoding delivery outcome %s: %w", envelope.ID, err)
	}

	span, found := envelope.Span()
	if !found {
		return fmt.Errorf("delivery outcome %s carries no readable trace context", envelope.ID)
	}
	work, err := span.Child()
	if err != nil {
		return err
	}

	cmd := Command{
		Principal: c.principal,
		// The message id is the idempotency key. It was derived from the release
		// message, so a redelivered outcome carries the key its first delivery
		// carried and the command is applied once.
		IdempotencyKey: envelope.ID,
		// This consumer read no version and asserts none. The version check that
		// matters is the store's, inside the transaction.
		ExpectedVersion: AnyVersion,
		Trace:           work,
	}

	switch envelope.Type {
	case delivery.TypeConfirmed:
		_, err = c.service.ConfirmDelivery(ctx, cmd, outcome.RequestID, outcome.PackageID)
	case delivery.TypeFailed:
		_, err = c.service.FailDelivery(ctx, cmd, outcome.RequestID, outcome.Reason)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownAction, envelope.Type)
	}

	if errors.Is(err, recordsrequest.ErrNotAwaitingDelivery) {
		// The request has already left release_pending, which means this fact
		// has already been applied under a different key -- a message the
		// deduplication could not recognize. The outcome is the one the fact
		// asked for, so there is nothing to do and nothing to report.
		return nil
	}
	return err
}
