// Package rebac stands up a relationship-based authorization model on an OpenFGA
// server and asks it questions. There is no authorization logic in this package:
// every allow and every deny in the tests is produced by the model in model.fga and
// the tuples seeded below, which is the point of the example. Application code that
// wants to know whether a user may read a document calls Allowed and gets an answer
// it can hand to an auditor along with the relationship path that produced it.
package rebac

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
)

// APIURLEnv names the environment variable holding the base URL of a running
// OpenFGA server, for example http://localhost:8080. The tests skip when it is
// unset so that a plain `go test ./...` needs no server and no container runtime.
const APIURLEnv = "OPENFGA_API_URL"

// modelJSON is the API representation of model.fga. The DSL is the source that
// humans review; this is the form the write API accepts. See the README for the
// one command that regenerates it.
//
//go:embed model.json
var modelJSON []byte

// Tuple is a single relationship fact: user, relation, object. A tuple set is the
// entire authorization state of the system -- there is nothing else to inspect.
type Tuple struct {
	User     string
	Relation string
	Object   string
	// Why records the sentence this tuple stands for, so a dump of the store reads
	// as an access register rather than as three columns of identifiers.
	Why string
}

// Scenario is a synthetic county public-records deployment: one organization, two
// nested folders, three documents, six people. Names, folders, and documents are
// invented for this example.
//
// The shape to notice is how little is written down. Nobody is granted access to a
// document because they work for the parks department; the department is related to
// a folder, the folder encloses the documents, and the model derives the rest.
var Scenario = []Tuple{
	// Staff roster for one department.
	{User: "user:dana", Relation: "member", Object: "organization:parks-department",
		Why: "Dana is on the parks department staff roster"},
	{User: "user:evan", Relation: "member", Object: "organization:parks-department",
		Why: "Evan is on the parks department staff roster"},

	// The public reading room folder, custodied by a records officer and readable by
	// the department that produces the records.
	{User: "organization:parks-department", Relation: "organization", Object: "folder:reading-room",
		Why: "the reading room holds parks department records"},
	{User: "user:rosa", Relation: "owner", Object: "folder:reading-room",
		Why: "Rosa is the records custodian for the reading room"},

	// A nested folder. It names no organization and no owner of its own; both arrive
	// through the parent relation.
	{User: "folder:reading-room", Relation: "parent", Object: "folder:permits",
		Why: "permits are filed inside the reading room"},

	// Three documents, each filed in a folder.
	{User: "folder:reading-room", Relation: "parent", Object: "document:bridge-inspection-2025",
		Why: "the bridge inspection report is filed in the reading room"},
	{User: "folder:permits", Relation: "parent", Object: "document:permit-application-88",
		Why: "permit application 88 is filed under permits"},
	{User: "folder:permits", Relation: "parent", Object: "document:sealed-affidavit",
		Why: "the sealed affidavit is filed under permits"},

	// Direct grants on one document.
	{User: "user:sam", Relation: "owner", Object: "document:permit-application-88",
		Why: "Sam wrote permit application 88"},
	{User: "user:tomas", Relation: "editor", Object: "document:permit-application-88",
		Why: "Tomas was asked to revise permit application 88"},

	// A revocation. Dana would reach the affidavit through the department's read on
	// the reading room; this tuple takes that away and says so on the record.
	{User: "user:dana", Relation: "blocked", Object: "document:sealed-affidavit",
		Why: "Dana is a party to the matter and is walled off from the affidavit"},
}

// NewStore creates a fresh store on the server at apiURL, writes the authorization
// model into it, and returns a client bound to both. Each run gets its own store so
// that runs never observe one another's tuples.
func NewStore(ctx context.Context, apiURL string) (*client.OpenFgaClient, error) {
	fga, err := client.NewSdkClient(&client.ClientConfiguration{ApiUrl: apiURL})
	if err != nil {
		return nil, fmt.Errorf("configuring client: %w", err)
	}

	store, err := fga.CreateStore(ctx).
		Body(client.ClientCreateStoreRequest{Name: "public-records-example"}).
		Execute()
	if err != nil {
		return nil, fmt.Errorf("creating store: %w", err)
	}
	if err := fga.SetStoreId(store.GetId()); err != nil {
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
	// Pinning the model identifier means every later check is answered by the model
	// this process wrote, not by whatever the store's latest model happens to be.
	if err := fga.SetAuthorizationModelId(model.GetAuthorizationModelId()); err != nil {
		return nil, fmt.Errorf("selecting authorization model: %w", err)
	}

	return fga, nil
}

// Seed writes the scenario tuples in a single transaction. A partial write would
// leave the store in a state no reviewer could reason about, so the server either
// accepts all of them or none.
func Seed(ctx context.Context, fga *client.OpenFgaClient, tuples []Tuple) error {
	writes := make([]client.ClientTupleKey, 0, len(tuples))
	for _, tuple := range tuples {
		writes = append(writes, openfga.TupleKey{
			User:     tuple.User,
			Relation: tuple.Relation,
			Object:   tuple.Object,
		})
	}
	if _, err := fga.Write(ctx).Body(client.ClientWriteRequest{Writes: writes}).Execute(); err != nil {
		return fmt.Errorf("writing %d tuple(s): %w", len(writes), err)
	}
	return nil
}

// Allowed answers a single authorization question. This is the whole of the
// integration an application needs: one call, one boolean, no rules restated in
// application code.
func Allowed(ctx context.Context, fga *client.OpenFgaClient, user, relation, object string) (bool, error) {
	response, err := fga.Check(ctx).Body(client.ClientCheckRequest{
		User:     user,
		Relation: relation,
		Object:   object,
	}).Execute()
	if err != nil {
		return false, fmt.Errorf("check %s %s %s: %w", user, relation, object, err)
	}
	return response.GetAllowed(), nil
}

// Visible answers the question agencies ask more often than check does: not "may
// this person open that record" but "what is this person able to see". The server
// walks the same relationships in reverse, so the list can never disagree with the
// individual checks.
func Visible(ctx context.Context, fga *client.OpenFgaClient, user, relation, objectType string) ([]string, error) {
	response, err := fga.ListObjects(ctx).Body(client.ClientListObjectsRequest{
		User:     user,
		Relation: relation,
		Type:     objectType,
	}).Execute()
	if err != nil {
		return nil, fmt.Errorf("list %s %s for %s: %w", objectType, relation, user, err)
	}
	return response.GetObjects(), nil
}
