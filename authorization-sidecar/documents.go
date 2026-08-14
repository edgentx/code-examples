package main

import (
	"errors"
	"sort"
	"sync"
)

// errNoSuchDocument is returned when a document identifier is not in the store.
// It is a not-found error, not an authorization error: this service has no
// concept of authorization. See the file comment in service.go.
var errNoSuchDocument = errors.New("no such document")

// document is the synthetic demo resource. Nothing here is real: the records,
// the case numbers, and the names are invented for this example.
type document struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Case  string `json:"case"`
}

// documentStore is an in-memory map guarded by a mutex. A real service would
// put a database behind this interface; the point of the example is what sits
// in front of the service, not what sits behind it.
type documentStore struct {
	mu   sync.RWMutex
	byID map[string]document
}

func newDocumentStore() *documentStore {
	seed := []document{
		{ID: "doc-1001", Title: "Permit application, Riverbend crossing", Case: "PRM-2024-0117"},
		{ID: "doc-1002", Title: "Inspection report, Riverbend crossing", Case: "PRM-2024-0117"},
		{ID: "doc-1003", Title: "Records request acknowledgment", Case: "REC-2024-0042"},
	}
	byID := make(map[string]document, len(seed))
	for _, d := range seed {
		byID[d.ID] = d
	}
	return &documentStore{byID: byID}
}

// list returns every document, ordered by identifier so responses are stable
// and tests can compare them without sorting first.
func (s *documentStore) list() []document {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]document, 0, len(s.byID))
	for _, d := range s.byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *documentStore) get(id string) (document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.byID[id]
	if !ok {
		return document{}, errNoSuchDocument
	}
	return d, nil
}

func (s *documentStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[id]; !ok {
		return errNoSuchDocument
	}
	delete(s.byID, id)
	return nil
}
