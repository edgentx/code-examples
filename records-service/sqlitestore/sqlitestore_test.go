package sqlitestore_test

import (
	"context"
	"testing"

	"github.com/edgentx/code-examples/records-service/sqlitestore"
	"github.com/edgentx/code-examples/records-service/storetest"
)

// TestContract runs the identical contract against a real SQLite database. The
// driver is pure Go, so this needs no cgo, no server and no build tag: the same
// assertions the twin passes are checked against SQL, real transactions, and a
// version check that has to hold across eight goroutines contending for the
// writer.
func TestContract(t *testing.T) {
	storetest.RunRepositoryContract(t, func(t *testing.T) storetest.Store {
		t.Helper()
		// A file in the test's temporary directory rather than ":memory:", so
		// each case gets a genuinely separate database and the schema, the
		// indexes and the transactions are all exercised the way they would be
		// on disk.
		store, err := sqlitestore.Open(context.Background(), t.TempDir()+"/records.db")
		if err != nil {
			t.Fatalf("opening store: %v", err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Errorf("closing store: %v", err)
			}
		})
		return store
	})
}
