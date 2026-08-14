package sqlitestore_test

import (
	"context"
	"testing"

	"github.com/edgentx/code-examples/hexagonal-service/permit"
	"github.com/edgentx/code-examples/hexagonal-service/repotest"
	"github.com/edgentx/code-examples/hexagonal-service/sqlitestore"
)

// TestContract runs the identical contract against a real SQLite database. The
// driver is pure Go, so this needs no cgo, no server and no build tag: the same
// assertions the twin passes are checked against SQL, transactionless
// optimistic locking and all.
func TestContract(t *testing.T) {
	repotest.RunRepositoryContract(t, func(t *testing.T) permit.Repository {
		t.Helper()
		// A file in the test's temporary directory rather than ":memory:", so
		// each case gets a genuinely separate database and the schema, the
		// index and the ORDER BY are all exercised the way they would be on disk.
		store, err := sqlitestore.Open(context.Background(), t.TempDir()+"/register.db")
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
