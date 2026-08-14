package rebac_test

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/openfga/go-sdk/client"

	rebac "github.com/edgentx/code-examples/rebac-authorization"
)

// fixture creates a store, writes the model, and seeds the scenario. Every test
// gets its own store, so a test can never be explained by a tuple another test
// wrote.
func fixture(t *testing.T) (context.Context, *client.OpenFgaClient) {
	t.Helper()

	apiURL := os.Getenv(rebac.APIURLEnv)
	if apiURL == "" {
		t.Skipf("%s is not set; start OpenFGA and set it to run the relationship checks (see README)",
			rebac.APIURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	fga, err := rebac.NewStore(ctx, apiURL)
	if err != nil {
		t.Fatalf("preparing store: %v", err)
	}
	if err := rebac.Seed(ctx, fga, rebac.Scenario); err != nil {
		t.Fatalf("seeding tuples: %v", err)
	}
	return ctx, fga
}

// TestCheck is the whole argument of this example. Each case names the
// relationship path it proves, so a failure says which rule moved rather than
// which boolean flipped.
func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		relation string
		object   string
		want     bool
		path     string
	}{
		{
			name:     "the author of a document can read it",
			user:     "user:sam",
			relation: "can_view",
			object:   "document:permit-application-88",
			want:     true,
			path:     "document#owner, through the owner branch of the can_view union",
		},
		{
			name:     "a direct grant lets a reviser edit",
			user:     "user:tomas",
			relation: "can_edit",
			object:   "document:permit-application-88",
			want:     true,
			path:     "one document#editor tuple, through the editor branch of can_edit",
		},
		{
			name:     "the records custodian inherits edit down two folders",
			user:     "user:rosa",
			relation: "can_edit",
			object:   "document:permit-application-88",
			want:     true,
			path:     "folder:reading-room#owner -> folder#editor -> folder:permits#editor from parent -> document#editor from parent",
		},
		{
			name:     "the records custodian inherits view one folder down",
			user:     "user:rosa",
			relation: "can_view",
			object:   "document:bridge-inspection-2025",
			want:     true,
			path:     "folder:reading-room#owner -> folder#editor -> document#editor from parent",
		},
		{
			name:     "department membership alone grants a read",
			user:     "user:dana",
			relation: "can_view",
			object:   "document:bridge-inspection-2025",
			want:     true,
			path:     "organization#member -> folder#viewer by member from organization -> document#viewer from parent",
		},
		{
			name:     "department membership reaches a nested folder",
			user:     "user:evan",
			relation: "can_view",
			object:   "document:permit-application-88",
			want:     true,
			path:     "organization#member -> folder:reading-room#viewer -> folder:permits#viewer from parent -> document#viewer from parent",
		},
		{
			name:     "someone with no relationship at all is denied",
			user:     "user:quinn",
			relation: "can_view",
			object:   "document:bridge-inspection-2025",
			want:     false,
			path:     "no tuple relates Quinn to any organization, folder, or document",
		},
		{
			name:     "an editor cannot delete the record",
			user:     "user:tomas",
			relation: "can_delete",
			object:   "document:permit-application-88",
			want:     false,
			path:     "can_delete is owner from parent only; editing is not custody",
		},
		{
			name:     "the author cannot delete the record either",
			user:     "user:sam",
			relation: "can_delete",
			object:   "document:permit-application-88",
			want:     false,
			path:     "retention: document#owner is absent from can_delete by design",
		},
		{
			name:     "the folder custodian can delete the record",
			user:     "user:rosa",
			relation: "can_delete",
			object:   "document:permit-application-88",
			want:     true,
			path:     "folder:reading-room#owner -> folder:permits#owner from parent -> document#can_delete by owner from parent",
		},
		{
			name:     "an editor cannot widen who else sees the record",
			user:     "user:tomas",
			relation: "can_share",
			object:   "document:permit-application-88",
			want:     false,
			path:     "can_share excludes the editor branch; sharing is an ownership action",
		},
		{
			name:     "the author can share the record",
			user:     "user:sam",
			relation: "can_share",
			object:   "document:permit-application-88",
			want:     true,
			path:     "document#owner, through the owner branch of can_share",
		},
		{
			name:     "a blocked user loses a read they would have inherited",
			user:     "user:dana",
			relation: "can_view",
			object:   "document:sealed-affidavit",
			want:     false,
			path:     "the blocked tuple subtracts Dana from can_view: (viewer or editor or owner) but not blocked",
		},
		{
			name:     "the block is what denies, not a missing grant",
			user:     "user:dana",
			relation: "viewer",
			object:   "document:sealed-affidavit",
			want:     true,
			path:     "the inherited document#viewer relation still holds; only can_view subtracts blocked",
		},
		{
			name:     "a colleague in the same department keeps the inherited read",
			user:     "user:evan",
			relation: "can_view",
			object:   "document:sealed-affidavit",
			want:     true,
			path:     "identical path to Dana's, minus the blocked tuple",
		},
	}

	ctx, fga := fixture(t)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := rebac.Allowed(ctx, fga, test.user, test.relation, test.object)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if allowed != test.want {
				t.Errorf("check(%s, %s, %s) = %t, want %t\nrelationship path: %s",
					test.user, test.relation, test.object, allowed, test.want, test.path)
			}
		})
	}
}

// TestListObjects answers the question an agency asks during discovery or a records
// audit: not "may this person open that file" but "what can this person see". The
// answer is derived from the same relationships, so it cannot drift from the
// individual checks above.
func TestListObjects(t *testing.T) {
	tests := []struct {
		name string
		user string
		want []string
		why  string
	}{
		{
			name: "a department member sees the two documents that are not walled off",
			user: "user:dana",
			want: []string{"document:bridge-inspection-2025", "document:permit-application-88"},
			why:  "department membership reaches every document in the reading room tree; the blocked tuple removes the affidavit",
		},
		{
			name: "someone outside the department sees nothing",
			user: "user:quinn",
			want: nil,
			why:  "an empty result is the correct answer, not an error",
		},
	}

	ctx, fga := fixture(t)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects, err := rebac.Visible(ctx, fga, test.user, "can_view", "document")
			if err != nil {
				t.Fatalf("list objects: %v", err)
			}
			sort.Strings(objects)

			if len(objects) != len(test.want) {
				t.Fatalf("visible documents = %v, want %v\n%s", objects, test.want, test.why)
			}
			for i := range objects {
				if objects[i] != test.want[i] {
					t.Fatalf("visible documents = %v, want %v\n%s", objects, test.want, test.why)
				}
			}
		})
	}
}
