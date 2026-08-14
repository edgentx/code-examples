package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// fakeStore is an in-memory Store with real etag semantics, so the concurrency
// behavior under test is the behavior the sidecar would show and not a stub
// that says yes to everything.
type fakeStore struct {
	mu      sync.Mutex
	values  map[string]json.RawMessage
	etags   map[string]int
	failGet error
	// failSaveOnce makes exactly one Save fail, which is how the tests
	// reproduce a lost etag race without any real concurrency.
	failSaveOnce error
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: map[string]json.RawMessage{}, etags: map[string]int{}}
}

func (f *fakeStore) Get(_ context.Context, key string) (json.RawMessage, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet != nil {
		return nil, "", f.failGet
	}
	value, ok := f.values[key]
	if !ok {
		return nil, "", nil
	}
	return value, strconv.Itoa(f.etags[key]), nil
}

func (f *fakeStore) Save(_ context.Context, key string, value json.RawMessage, etag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSaveOnce != nil {
		err := f.failSaveOnce
		f.failSaveOnce = nil
		return err
	}
	_, exists := f.values[key]
	switch {
	case etag == "" && exists:
		// first-write with no etag means "create": the key must not exist yet.
		return fmt.Errorf("%w: %s already exists", ErrETagConflict, key)
	case etag != "" && etag != strconv.Itoa(f.etags[key]):
		return fmt.Errorf("%w: %s", ErrETagConflict, key)
	}
	f.values[key] = value
	f.etags[key]++
	return nil
}

// fakePublisher records what a handler asked to publish.
type fakePublisher struct {
	events []publishedEvent
	err    error
}

type publishedEvent struct {
	topic string
	event CloudEvent
}

func (f *fakePublisher) Publish(_ context.Context, topic string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, publishedEvent{topic: topic, event: CloudEvent{Data: body}})
	return nil
}

func (f *fakePublisher) PublishEvent(_ context.Context, topic string, event CloudEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, publishedEvent{topic: topic, event: event})
	return nil
}

func (f *fakePublisher) last() publishedEvent {
	if len(f.events) == 0 {
		return publishedEvent{}
	}
	return f.events[len(f.events)-1]
}

var errStoreDown = errors.New("state store unreachable")
