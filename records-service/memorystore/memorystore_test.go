package memorystore_test

import (
	"testing"

	"github.com/edgentx/code-examples/records-service/memorystore"
	"github.com/edgentx/code-examples/records-service/storetest"
)

// TestContract runs the whole port contract against the in-memory twin. It is
// the same suite the SQLite adapter runs, which is the only reason the twin may
// stand in for the store anywhere else in this example.
func TestContract(t *testing.T) {
	storetest.RunRepositoryContract(t, func(*testing.T) storetest.Store {
		return memorystore.New()
	})
}
