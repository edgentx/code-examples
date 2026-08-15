// Package authztest holds the executable contract for authz.Store: one exported
// suite that the in-memory adapter and the OpenFGA-backed adapter both run.
//
// The suite is what keeps the two in step. The in-memory adapter resolves the
// relations in Go so the service can be tested and the console can be run with
// nothing else installed; the OpenFGA adapter asks a server that reads
// model.fga. If an edit to one is not made to the other, the shared suite fails
// on whichever was left behind, which is the only reliable way to run a fast
// twin of an authorization system without the twin quietly becoming fiction.
//
// Every action is asserted twice: once for a principal who may take it and once
// for a principal who may not. A suite that only proves allows cannot tell a
// working model from one that permits everything.
package authztest

import (
	"context"
	"testing"

	"github.com/edgentx/code-examples/records-service/authz"
)

// The synthetic office this suite describes. Two offices exist so that "a
// reviewer" is never enough on its own: a reviewer of the wrong office must
// reach nothing.
const (
	Office      = "records-office"
	OtherOffice = "parks-office"

	// RequestID is filed with Office by Requester.
	RequestID = "PRR-2026-0041"
	// OrphanRequestID is a request nobody wrote an office relation for. It
	// exists to prove a missing relationship denies rather than defaults.
	OrphanRequestID = "PRR-2026-0099"

	Clerk         = "user:c.hall"
	Reviewer      = "user:r.okafor"
	Requester     = "user:m.alvarez"
	OtherReviewer = "user:d.mensah"
	Stranger      = "user:t.nowak"
	Fulfillment   = "service:records-fulfillment"
)

// Scenario is the whole authorization state the suite runs against. It is short
// on purpose: seven facts decide every case below, and the ones that are not
// written are as load-bearing as the ones that are.
var Scenario = []authz.Tuple{
	{User: Clerk, Relation: "clerk", Object: authz.OfficeObject(Office),
		Why: "C. Hall works the records office intake desk"},
	{User: Reviewer, Relation: "reviewer", Object: authz.OfficeObject(Office),
		Why: "R. Okafor is a records officer for the records office"},
	{User: Fulfillment, Relation: "fulfillment", Object: authz.OfficeObject(Office),
		Why: "the records office's release packages are assembled by this service"},
	{User: OtherReviewer, Relation: "reviewer", Object: authz.OfficeObject(OtherOffice),
		Why: "D. Mensah is a records officer for the parks office"},

	{User: authz.OfficeObject(Office), Relation: "office", Object: authz.RequestObject(RequestID),
		Why: "the bridge inspection request was filed with the records office"},
	{User: Requester, Relation: "requester", Object: authz.RequestObject(RequestID),
		Why: "M. Alvarez filed the bridge inspection request"},
}

// RunCheckerContract exercises the authz.Store port against an implementation.
// newStore is called once per case and must return an empty, unseeded store; the
// suite seeds it.
func RunCheckerContract(t *testing.T, newStore func(t *testing.T) authz.Store) {
	t.Helper()

	t.Run("decisions", func(t *testing.T) {
		store := seeded(t, newStore)
		for _, decision := range decisions() {
			t.Run(decision.name, func(t *testing.T) {
				allowed, err := store.Allowed(context.Background(), decision.principal,
					decision.action, decision.object)
				if err != nil {
					t.Fatalf("Allowed: %v", err)
				}
				if allowed != decision.want {
					t.Errorf("Allowed(%s, %s, %s) = %t, want %t",
						decision.principal, decision.action, decision.object,
						allowed, decision.want)
				}
			})
		}
	})

	t.Run("permitted actions", func(t *testing.T) {
		store := seeded(t, newStore)
		request := authz.RequestObject(RequestID)
		tests := []struct {
			name      string
			principal string
			want      []authz.Action
		}{
			{
				name:      "a requester may only read",
				principal: Requester,
				want:      []authz.Action{authz.ActionRead},
			},
			{
				name:      "a clerk may read and acknowledge",
				principal: Clerk,
				want:      []authz.Action{authz.ActionRead, authz.ActionAcknowledge},
			},
			{
				name:      "a reviewer may decide the answer but not report the delivery",
				principal: Reviewer,
				want: []authz.Action{
					authz.ActionRead,
					authz.ActionAcknowledge,
					authz.ActionAssignReviewer,
					authz.ActionRelease,
					authz.ActionDeny,
				},
			},
			{
				name:      "the fulfillment service may only report the delivery",
				principal: Fulfillment,
				want:      []authz.Action{authz.ActionRecordDelivery},
			},
			{
				name:      "a reviewer from another office may do nothing",
				principal: OtherReviewer,
				want:      nil,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				got, err := store.Permitted(context.Background(), test.principal, request)
				if err != nil {
					t.Fatalf("Permitted: %v", err)
				}
				assertActions(t, got, test.want)
			})
		}
	})

	t.Run("writing a recorded fact again is not an error", func(t *testing.T) {
		// The service writes the same two facts every time a submission is
		// retried, so a store that rejects a repeat turns an idempotent command
		// into a failure the operator sees.
		store := seeded(t, newStore)

		if err := store.Write(context.Background(), Scenario); err != nil {
			t.Fatalf("rewriting the scenario: %v", err)
		}

		allowed, err := store.Allowed(context.Background(), Reviewer, authz.ActionRelease,
			authz.RequestObject(RequestID))
		if err != nil {
			t.Fatalf("Allowed: %v", err)
		}
		if !allowed {
			t.Error("rewriting the facts changed a decision")
		}
	})
}

// decision is one question and the answer the model owes.
type decision struct {
	name      string
	principal string
	action    authz.Action
	object    string
	want      bool
}

// decisions is the allow-and-deny matrix. Reading it top to bottom is reading
// the access policy of the service.
func decisions() []decision {
	request := authz.RequestObject(RequestID)
	orphan := authz.RequestObject(OrphanRequestID)
	office := authz.OfficeObject(Office)

	return []decision{
		// Reading follows ownership or office membership.
		{"a requester reads their own request", Requester, authz.ActionRead, request, true},
		{"a clerk reads a request of their office", Clerk, authz.ActionRead, request, true},
		{"a reviewer reads a request of their office", Reviewer, authz.ActionRead, request, true},
		{"a reviewer of another office reads nothing", OtherReviewer, authz.ActionRead, request, false},
		{"a stranger reads nothing", Stranger, authz.ActionRead, request, false},
		{"the fulfillment service is not a reader", Fulfillment, authz.ActionRead, request, false},

		// The receipt notice is clerical work.
		{"a clerk acknowledges", Clerk, authz.ActionAcknowledge, request, true},
		{"a reviewer acknowledges", Reviewer, authz.ActionAcknowledge, request, true},
		{"a requester does not acknowledge their own request", Requester,
			authz.ActionAcknowledge, request, false},

		// Deciding the answer requires the reviewer relation.
		{"a reviewer assigns an officer", Reviewer, authz.ActionAssignReviewer, request, true},
		{"a clerk does not assign an officer", Clerk, authz.ActionAssignReviewer, request, false},
		{"a reviewer releases records", Reviewer, authz.ActionRelease, request, true},
		{"a clerk does not release records", Clerk, authz.ActionRelease, request, false},
		{"a requester does not release records", Requester, authz.ActionRelease, request, false},
		{"a reviewer denies a request", Reviewer, authz.ActionDeny, request, true},
		{"a clerk does not deny a request", Clerk, authz.ActionDeny, request, false},

		// The compensation path. Whether a package arrived is a report, and only
		// the office's fulfillment service may make it.
		{"the fulfillment service records a delivery outcome", Fulfillment,
			authz.ActionRecordDelivery, request, true},
		{"a reviewer does not record their own delivery outcome", Reviewer,
			authz.ActionRecordDelivery, request, false},
		{"a clerk does not record a delivery outcome", Clerk,
			authz.ActionRecordDelivery, request, false},
		{"a requester does not record a delivery outcome", Requester,
			authz.ActionRecordDelivery, request, false},

		// Filing is asked against the office, before the request exists.
		{"a clerk files a request with their office", Clerk, authz.ActionSubmit, office, true},
		{"a reviewer files a request with their office", Reviewer, authz.ActionSubmit, office, true},
		{"a reviewer of another office does not file here", OtherReviewer,
			authz.ActionSubmit, office, false},
		{"a stranger does not file here", Stranger, authz.ActionSubmit, office, false},

		// A request with no office relation is reachable by nobody. There is no
		// fallback branch in the model, so an unrelated object denies rather
		// than defaulting to the last rule that matched.
		{"an unrelated request is unreadable by a reviewer", Reviewer, authz.ActionRead, orphan, false},
		{"an unrelated request is unreadable by its would-be requester", Requester,
			authz.ActionRead, orphan, false},
		{"an unrelated request cannot be released", Reviewer, authz.ActionRelease, orphan, false},
	}
}

// seeded returns a store with the scenario written into it.
func seeded(t *testing.T, newStore func(t *testing.T) authz.Store) authz.Store {
	t.Helper()
	store := newStore(t)
	if err := store.Write(context.Background(), Scenario); err != nil {
		t.Fatalf("seeding the scenario: %v", err)
	}
	return store
}

// assertActions compares a permitted-action list against the expected one,
// order included: the order is the order the console renders controls in.
func assertActions(t *testing.T, got, want []authz.Action) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("permitted actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("permitted actions = %v, want %v", got, want)
		}
	}
}
