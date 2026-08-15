package recordsrequest

import (
	"encoding/json"
	"fmt"
)

// The event codec is part of the storage contract, so it lives with the events
// rather than in an adapter: both stores write the same bytes for the same
// fact, and a stream written by one can be read by the other. EventName is the
// discriminator, which is why it never changes once an event is in production.

// EncodeEvent renders one event as the JSON payload written to the event store
// and carried in the CloudEvents envelope.
func EncodeEvent(event Event) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", event.EventName(), err)
	}
	return payload, nil
}

// DecodeEvent rebuilds an event from its stored name and payload. An unknown
// name is ErrUnknownEvent rather than a nil event: a stream the current code
// cannot fully understand must fail loudly, not load into a plausible-looking
// wrong state.
func DecodeEvent(name string, payload []byte) (Event, error) {
	var (
		event Event
		err   error
	)
	switch name {
	case "records_request.submitted":
		event, err = decodeInto[Submitted](payload)
	case "records_request.acknowledged":
		event, err = decodeInto[Acknowledged](payload)
	case "records_request.reviewer_assigned":
		event, err = decodeInto[ReviewerAssigned](payload)
	case "records_request.fulfilled":
		event, err = decodeInto[Fulfilled](payload)
	case "records_request.delivery_confirmed":
		event, err = decodeInto[DeliveryConfirmed](payload)
	case "records_request.release_failed":
		event, err = decodeInto[ReleaseFailed](payload)
	case "records_request.denied":
		event, err = decodeInto[Denied](payload)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownEvent, name)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return event, nil
}

// decodeInto unmarshals a payload into one concrete event type.
func decodeInto[E Event](payload []byte) (Event, error) {
	var event E
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	return event, nil
}
