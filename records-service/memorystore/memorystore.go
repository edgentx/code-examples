// Package memorystore is the in-memory twin of the event store. It is a real
// implementation of recordsrequest.Repository and recordsrequest.Projection, not
// a mock: it appends to streams, enforces the version check, records idempotency
// keys, writes outbox entries under the same lock as the events, and proves it
// by passing the same contract suite the SQLite adapter passes.
//
// That is what makes it usable as the test double for everything above it. A
// mock asserts that a call was made; this asserts that the behavior was right,
// and it costs a test nothing to run.
package memorystore

import (
	"context"
	"sync"

	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// Store is an in-memory event store with an outbox and a read model. The zero
// value is not usable; call New. It is safe for concurrent use, because the twin
// has to behave like the store it stands in for and a database is.
type Store struct {
	mu sync.Mutex

	// events is the log, in commit order. Sequence is the index plus one, which
	// is what makes it monotonic and never reused.
	events []recordsrequest.Stored
	// versions is the current version of each stream, so the lock check does not
	// have to scan the log.
	versions map[string]int
	// dispatched records which events have been published.
	dispatched map[int64]bool
	// recorded maps an idempotency key to the result its append produced.
	recorded map[string]recordsrequest.Result

	// The read model, which holds nothing that is not derivable from events.
	summaries  map[string]recordsrequest.Summary
	checkpoint int64
}

// New returns an empty store.
func New() *Store {
	return &Store{
		versions:   make(map[string]int),
		dispatched: make(map[int64]bool),
		recorded:   make(map[string]recordsrequest.Result),
		summaries:  make(map[string]recordsrequest.Summary),
	}
}

// Load rebuilds a request by replaying its stream.
func (s *Store) Load(_ context.Context, requestID string) (*recordsrequest.Request, error) {
	s.mu.Lock()
	stream := make([]recordsrequest.Stored, 0, 8)
	for _, stored := range s.events {
		if stored.RequestID == requestID {
			stream = append(stream, stored)
		}
	}
	s.mu.Unlock()

	if len(stream) == 0 {
		return nil, recordsrequest.ErrNotFound
	}
	history := make([]recordsrequest.Event, 0, len(stream))
	for _, stored := range stream {
		event, err := stored.Event()
		if err != nil {
			return nil, err
		}
		history = append(history, event)
	}
	return recordsrequest.FromHistory(history)
}

// Recorded reports the result of an earlier append under an idempotency key.
func (s *Store) Recorded(_ context.Context, key string) (recordsrequest.Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, found := s.recorded[key]
	if !found {
		return recordsrequest.Result{}, false, nil
	}
	result.Replayed = true
	return result, true, nil
}

// Append writes events under one lock, which is this adapter's transaction.
// Every check that could reject the append happens before the first mutation, so
// a rejected append leaves nothing behind -- no event, and no outbox entry.
func (s *Store) Append(_ context.Context, write recordsrequest.Append) (recordsrequest.Result, error) {
	if write.IdempotencyKey == "" {
		return recordsrequest.Result{}, recordsrequest.ErrNoIdempotencyKey
	}
	if len(write.Events) == 0 {
		return recordsrequest.Result{}, recordsrequest.ErrNoChanges
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if result, found := s.recorded[write.IdempotencyKey]; found {
		result.Replayed = true
		return result, nil
	}
	if s.versions[write.RequestID] != write.ExpectedVersion {
		return recordsrequest.Result{}, recordsrequest.ErrVersionConflict
	}

	staged := make([]recordsrequest.Stored, 0, len(write.Events))
	version := write.ExpectedVersion
	for _, event := range write.Events {
		payload, err := recordsrequest.EncodeEvent(event)
		if err != nil {
			// Nothing has been mutated yet, so failing here leaves the store
			// exactly as the caller found it.
			return recordsrequest.Result{}, err
		}
		version++
		staged = append(staged, recordsrequest.Stored{
			Sequence:  int64(len(s.events)+len(staged)) + 1,
			RequestID: write.RequestID,
			Version:   version,
			Name:      event.EventName(),
			Payload:   payload,
			Metadata: recordsrequest.Metadata{
				TraceParent:    write.TraceParent,
				IdempotencyKey: write.IdempotencyKey,
			},
			RecordedAt: event.OccurredAt(),
		})
	}

	for _, stored := range staged {
		s.events = append(s.events, stored)
		// The outbox entry is created in the same critical section as the event.
		// There is no moment at which one exists without the other.
		s.dispatched[stored.Sequence] = false
	}
	s.versions[write.RequestID] = version

	result := recordsrequest.Result{RequestID: write.RequestID, Version: version}
	s.recorded[write.IdempotencyKey] = result
	return result, nil
}

// Stream reads committed events after a sequence, in commit order.
func (s *Store) Stream(_ context.Context, afterSequence int64,
	limit int) ([]recordsrequest.Stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch := make([]recordsrequest.Stored, 0, limit)
	for _, stored := range s.events {
		if stored.Sequence <= afterSequence {
			continue
		}
		if len(batch) == limit {
			break
		}
		batch = append(batch, copyStored(stored))
	}
	return batch, nil
}

// PendingOutbox returns committed events that have not been published.
func (s *Store) PendingOutbox(_ context.Context, limit int) ([]recordsrequest.Stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := make([]recordsrequest.Stored, 0, limit)
	for _, stored := range s.events {
		if s.dispatched[stored.Sequence] {
			continue
		}
		if len(pending) == limit {
			break
		}
		pending = append(pending, copyStored(stored))
	}
	return pending, nil
}

// MarkDispatched records that an event's message was published. Marking one that
// is already marked is not an error: a relay that published, died before
// marking, and published again on restart will mark twice.
func (s *Store) MarkDispatched(_ context.Context, sequence int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, known := s.dispatched[sequence]; known {
		s.dispatched[sequence] = true
	}
	return nil
}

// Checkpoint is the sequence the read model has consumed through.
func (s *Store) Checkpoint(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.checkpoint, nil
}

// Save writes summaries and advances the checkpoint together.
func (s *Store) Save(_ context.Context, summaries []recordsrequest.Summary,
	throughSequence int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if throughSequence <= s.checkpoint {
		// Already applied. Repeating a batch is normal for a projector that
		// crashed after writing and before recording its progress.
		return nil
	}
	for _, summary := range summaries {
		s.summaries[summary.RequestID] = summary
	}
	s.checkpoint = throughSequence
	return nil
}

// Summaries returns the read model in submission order.
func (s *Store) Summaries(_ context.Context) ([]recordsrequest.Summary, error) {
	s.mu.Lock()
	rows := make([]recordsrequest.Summary, 0, len(s.summaries))
	for _, summary := range s.summaries {
		rows = append(rows, summary)
	}
	s.mu.Unlock()

	recordsrequest.SortSummaries(rows)
	return rows, nil
}

// copyStored hands out a payload the caller cannot write through into the log.
func copyStored(stored recordsrequest.Stored) recordsrequest.Stored {
	stored.Payload = append([]byte(nil), stored.Payload...)
	return stored
}
