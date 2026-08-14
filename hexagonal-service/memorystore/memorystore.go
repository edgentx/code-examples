// Package memorystore is a driven adapter that keeps the permit register in a
// map. It is a real implementation of permit.Repository, not a mock: it enforces
// uniqueness, stamps versions, rejects stale updates and orders query results,
// and it proves it by passing the same contract suite the SQLite adapter passes.
//
// That is what makes it usable as the test double for everything above it. A
// mock asserts that a call was made; this asserts that the behavior was right.
package memorystore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/permit"
)

// Store is an in-memory permit register. The zero value is not usable; call New.
// It is safe for concurrent use, because the twin has to behave like the store
// it stands in for and a database is.
type Store struct {
	mu      sync.RWMutex
	permits map[string]permit.Permit
}

// New returns an empty register.
func New() *Store {
	return &Store{permits: make(map[string]permit.Permit)}
}

// Register adds a permit that is not on the register yet.
func (s *Store) Register(_ context.Context, p permit.Permit) (permit.Permit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.permits[p.Number]; exists {
		return permit.Permit{}, permit.ErrDuplicateNumber
	}
	p.Version = 1
	s.permits[p.Number] = p
	return p, nil
}

// Update replaces a permit whose version still matches the stored one.
func (s *Store) Update(_ context.Context, p permit.Permit) (permit.Permit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, exists := s.permits[p.Number]
	if !exists {
		return permit.Permit{}, permit.ErrNotFound
	}
	if stored.Version != p.Version {
		return permit.Permit{}, permit.ErrVersionConflict
	}
	p.Version = stored.Version + 1
	s.permits[p.Number] = p
	return p, nil
}

// ByNumber returns one permit.
func (s *Store) ByNumber(_ context.Context, number string) (permit.Permit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stored, exists := s.permits[number]
	if !exists {
		return permit.Permit{}, permit.ErrNotFound
	}
	return stored, nil
}

// ExpiringBefore returns active permits expiring before the cutoff, soonest
// first, ties broken by number.
func (s *Store) ExpiringBefore(_ context.Context, cutoff time.Time) ([]permit.Permit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	due := make([]permit.Permit, 0, len(s.permits))
	for _, stored := range s.permits {
		if stored.Status == permit.StatusActive && stored.ExpiresOn.Before(cutoff) {
			due = append(due, stored)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].ExpiresOn.Equal(due[j].ExpiresOn) {
			return due[i].Number < due[j].Number
		}
		return due[i].ExpiresOn.Before(due[j].ExpiresOn)
	})
	return due, nil
}
