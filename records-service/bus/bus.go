// Package bus is the message transport the two services share.
//
// It delivers in process and synchronously, which is a test decision rather
// than an architectural one: a broker sits here in a deployment, and nothing
// above this package knows which it is talking to. Delivering synchronously is
// what lets the acceptance criteria assert an outcome without sleeping, and a
// test that sleeps is a test that will be flaky on a loaded build machine.
//
// What the bus deliberately does not provide is deduplication. Redelivery is
// normal -- it is what the relay does after a crash -- so every consumer here
// deduplicates on the CloudEvents id. Putting that in the transport would hide
// the property the consumers are supposed to demonstrate.
package bus

import (
	"context"
	"fmt"
	"sync"

	"github.com/edgentx/code-examples/records-service/cloudevent"
)

// Handler processes one message. Returning an error means the message was not
// processed and the publisher is told, which is how a relay learns to leave the
// entry undispatched and try again.
type Handler func(ctx context.Context, envelope cloudevent.Envelope) error

// Bus routes messages to the handlers subscribed to their type.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{handlers: make(map[string][]Handler)}
}

// Subscribe registers a handler for one CloudEvents type.
func (b *Bus) Subscribe(eventType string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// Publish delivers a message to every handler subscribed to its type. A type
// nobody subscribed to is delivered nowhere and is not an error: services are
// supposed to be able to emit facts that nothing currently listens for.
func (b *Bus) Publish(ctx context.Context, envelope cloudevent.Envelope) error {
	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers[envelope.Type]))
	copy(handlers, b.handlers[envelope.Type])
	b.mu.RUnlock()

	for _, handler := range handlers {
		if err := handler(ctx, envelope); err != nil {
			return fmt.Errorf("handling %s (%s): %w", envelope.Type, envelope.ID, err)
		}
	}
	return nil
}
