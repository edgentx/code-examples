package openfgaauthz_test

import (
	"context"
	"os"
	"testing"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/authztest"
	"github.com/edgentx/code-examples/records-service/openfgaauthz"
)

// TestContract runs the identical authorization contract against a live OpenFGA
// server reading model.fga. It skips when no server is configured, so a plain
// `go test ./...` needs no container runtime; continuous integration starts a
// server, sets OPENFGA_API_URL, and the same allow-and-deny matrix is answered
// by the model rather than by Go.
func TestContract(t *testing.T) {
	apiURL := os.Getenv(openfgaauthz.APIURLEnv)
	if apiURL == "" {
		t.Skipf("set %s to a running OpenFGA server to run this suite", openfgaauthz.APIURLEnv)
	}

	authztest.RunCheckerContract(t, func(t *testing.T) authz.Store {
		t.Helper()
		// A store per case, so no case can observe the tuples another one wrote.
		store, err := openfgaauthz.Open(context.Background(), apiURL)
		if err != nil {
			t.Fatalf("opening an OpenFGA store: %v", err)
		}
		return store
	})
}
