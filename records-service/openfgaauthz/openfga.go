// Package openfgaauthz is the second authorization adapter: the same
// authz.Store port, answered by a running OpenFGA server against the model in
// model.fga.
//
// Nothing in this package decides anything. It creates a store, writes the
// model, writes relationship facts, and forwards questions. Every allow and
// every deny the tests observe is produced by the model, which is the property
// that makes an access decision explainable to an auditor: the answer can be
// traced back to named tuples and one reviewable model file.
package openfgaauthz

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"

	"github.com/edgentx/code-examples/records-service/authz"
)

// APIURLEnv names the environment variable holding the base URL of a running
// OpenFGA server, for example http://localhost:8080. The tests skip when it is
// unset so that a plain `go test ./...` needs no server and no container
// runtime; continuous integration sets it and the same contract runs for real.
const APIURLEnv = "OPENFGA_API_URL"

// modelJSON is the API representation of model.fga. The DSL is the source that
// humans review; this is the form the write API accepts. See the README for the
// one command that regenerates it.
//
//go:embed model.json
var modelJSON []byte

// Store is an OpenFGA-backed authorization store.
type Store struct {
	fga *client.OpenFgaClient
}

// Open creates a fresh store on the server at apiURL, writes the authorization
// model into it, and returns an adapter bound to both. Each run gets its own
// store so that runs never observe one another's tuples.
func Open(ctx context.Context, apiURL string) (*Store, error) {
	fga, err := client.NewSdkClient(&client.ClientConfiguration{ApiUrl: apiURL})
	if err != nil {
		return nil, fmt.Errorf("configuring client: %w", err)
	}

	created, err := fga.CreateStore(ctx).
		Body(client.ClientCreateStoreRequest{Name: "records-service-example"}).
		Execute()
	if err != nil {
		return nil, fmt.Errorf("creating store: %w", err)
	}
	if err := fga.SetStoreId(created.GetId()); err != nil {
		return nil, fmt.Errorf("selecting store: %w", err)
	}

	var request client.ClientWriteAuthorizationModelRequest
	if err := json.Unmarshal(modelJSON, &request); err != nil {
		return nil, fmt.Errorf("decoding model.json: %w", err)
	}
	model, err := fga.WriteAuthorizationModel(ctx).Body(request).Execute()
	if err != nil {
		return nil, fmt.Errorf("writing authorization model: %w", err)
	}
	// Pinning the model identifier means every later check is answered by the
	// model this process wrote, not by whatever the store's latest model happens
	// to be.
	if err := fga.SetAuthorizationModelId(model.GetAuthorizationModelId()); err != nil {
		return nil, fmt.Errorf("selecting authorization model: %w", err)
	}

	return &Store{fga: fga}, nil
}

// Write records relationship facts, skipping the ones already recorded.
//
// The filtering is what makes the port's idempotency promise true here: OpenFGA
// rejects a write of a tuple that already exists, and the service writes the
// same two tuples again whenever a submission is retried. Reading first turns a
// retry into a no-op instead of a failure.
func (s *Store) Write(ctx context.Context, tuples []authz.Tuple) error {
	writes := make([]client.ClientTupleKey, 0, len(tuples))
	for _, tuple := range tuples {
		recorded, err := s.exists(ctx, tuple)
		if err != nil {
			return err
		}
		if recorded {
			continue
		}
		writes = append(writes, openfga.TupleKey{
			User:     tuple.User,
			Relation: tuple.Relation,
			Object:   tuple.Object,
		})
	}
	if len(writes) == 0 {
		return nil
	}
	// One transaction. A partial write would leave a request related to an
	// office but with no requester, which is a state no reviewer could reason
	// about.
	if _, err := s.fga.Write(ctx).
		Body(client.ClientWriteRequest{Writes: writes}).Execute(); err != nil {
		return fmt.Errorf("writing %d tuple(s): %w", len(writes), err)
	}
	return nil
}

// Allowed answers one authorization question. This is the whole of the
// integration the service needs: one call, one boolean, no rules restated in
// application code.
func (s *Store) Allowed(ctx context.Context, principal string, action authz.Action,
	object string) (bool, error) {
	response, err := s.fga.Check(ctx).Body(client.ClientCheckRequest{
		User:     principal,
		Relation: string(action),
		Object:   object,
	}).Execute()
	if err != nil {
		return false, fmt.Errorf("check %s %s %s: %w", principal, action, object, err)
	}
	return response.GetAllowed(), nil
}

// Permitted lists the actions a principal may take on a request.
func (s *Store) Permitted(ctx context.Context, principal, object string) ([]authz.Action, error) {
	return authz.PermittedVia(ctx, s, principal, object)
}

// exists reports whether one exact tuple is already recorded.
func (s *Store) exists(ctx context.Context, tuple authz.Tuple) (bool, error) {
	response, err := s.fga.Read(ctx).Body(client.ClientReadRequest{
		User:     &tuple.User,
		Relation: &tuple.Relation,
		Object:   &tuple.Object,
	}).Execute()
	if err != nil {
		return false, fmt.Errorf("reading tuple %s %s %s: %w",
			tuple.User, tuple.Relation, tuple.Object, err)
	}
	return len(response.GetTuples()) > 0, nil
}
