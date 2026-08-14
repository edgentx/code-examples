package memorystore_test

import (
	"testing"

	"github.com/edgentx/code-examples/hexagonal-service/memorystore"
	"github.com/edgentx/code-examples/hexagonal-service/permit"
	"github.com/edgentx/code-examples/hexagonal-service/repotest"
)

// TestContract runs the port's whole contract against the in-memory twin. It is
// the only reason the twin can be trusted anywhere else in the example: it is
// held to the same promises as the relational store, not to a looser set.
func TestContract(t *testing.T) {
	repotest.RunRepositoryContract(t, func(t *testing.T) permit.Repository {
		return memorystore.New()
	})
}
