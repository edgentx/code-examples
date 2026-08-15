package authz_test

import (
	"testing"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/authztest"
)

// TestContract runs the whole authorization contract against the in-memory
// adapter. It is the same suite the OpenFGA adapter runs, which is the only
// reason this adapter may stand in for a server anywhere else in this example.
func TestContract(t *testing.T) {
	authztest.RunCheckerContract(t, func(*testing.T) authz.Store {
		return authz.NewMemory()
	})
}
