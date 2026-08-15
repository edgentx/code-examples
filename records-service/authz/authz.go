// Package authz is the authorization port and its in-memory adapter.
//
// Access here is relationship-based, not role-based: nothing in the service
// asks "is this person an administrator". It asks whether a path exists from a
// principal to an object with a named relation, and the relations are defined
// once in an authorization model rather than restated in each handler. The model
// method -- what a relation means, why a union beats a conditional, how a
// deny is written down as a fact -- is the subject of
// ../../rebac-authorization; this package applies it to the records domain and
// puts it behind a port so the service can be tested without a server running.
//
// The model this port speaks for, in one paragraph. A records request belongs to
// an office and names a requester. The requester may read their own request and
// do nothing else to it. Office staff hold one of two relations: a clerk may
// read and send the statutory receipt notice; a reviewer may additionally assign
// an officer, release records, and deny a request. The delivery outcome that
// closes or compensates a release can be recorded only by the fulfillment
// service registered to that office -- not by a clerk, not by a reviewer, and
// not by the requester, because it is a report of what happened rather than a
// decision anybody is entitled to make.
package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrDenied is what the service returns when a principal may not do what it
// asked. It is a named error rather than a boolean returned upward, so no
// handler can forget to check the boolean.
var ErrDenied = errors.New("not authorized")

// Action is one relation the application asks about. The values are the relation
// names in the authorization model: application code and the model use one
// vocabulary, so a reviewer comparing them is comparing like with like.
type Action string

const (
	// ActionSubmit is asked against an office: who may file a request into it.
	ActionSubmit Action = "can_submit"
	// ActionRead is asked against a request.
	ActionRead Action = "can_read"
	// ActionAcknowledge sends the statutory receipt notice.
	ActionAcknowledge Action = "can_acknowledge"
	// ActionAssignReviewer routes a request to a records officer.
	ActionAssignReviewer Action = "can_assign_reviewer"
	// ActionRelease releases responsive records for delivery.
	ActionRelease Action = "can_release"
	// ActionDeny withholds records under an exemption.
	ActionDeny Action = "can_deny"
	// ActionRecordDelivery reports the outcome of a release. It is the
	// compensation path's actor check: only the fulfillment service may say
	// whether a package arrived.
	ActionRecordDelivery Action = "can_record_delivery"
)

// RequestActions are the actions asked against a records request, in the order
// the console renders their controls. Permitted returns a subset of this list,
// which is how the console learns what to show without knowing any rule.
var RequestActions = []Action{
	ActionRead,
	ActionAcknowledge,
	ActionAssignReviewer,
	ActionRelease,
	ActionDeny,
	ActionRecordDelivery,
}

// Object names something access is asked about. Objects are typed strings in the
// authorization model's own form -- "records_request:PRR-2026-0041" -- so a
// tuple dump and a check in the code read the same.
func RequestObject(requestID string) string { return "records_request:" + requestID }

// OfficeObject names an office.
func OfficeObject(officeID string) string { return "office:" + officeID }

// UserPrincipal names a person.
func UserPrincipal(userID string) string { return "user:" + userID }

// ServicePrincipal names a non-human caller.
func ServicePrincipal(serviceID string) string { return "service:" + serviceID }

// Tuple is a single relationship fact: user, relation, object. The tuple set is
// the entire authorization state -- there is nothing else to inspect, and every
// grant and every revocation is one row a reviewer can read.
type Tuple struct {
	User     string
	Relation string
	Object   string
	// Why records the sentence the tuple stands for, so a dump of the store
	// reads as an access register rather than three columns of identifiers.
	Why string
}

// Checker is the driven port: the service asks, something else decides.
type Checker interface {
	// Allowed answers one question. An error means the decision could not be
	// made, which is not the same as a deny and must never be treated as one.
	Allowed(ctx context.Context, principal string, action Action, object string) (bool, error)

	// Permitted answers "what may this principal do to this request", which is
	// the question a console asks so it can render controls the operator is
	// actually able to use. It is derived from the same relations as Allowed,
	// so a button that appears is a button whose action will be permitted.
	Permitted(ctx context.Context, principal, object string) ([]Action, error)
}

// Store is a Checker whose relationships can be written. Creating a request
// writes the two facts that make it reachable -- the office it belongs to and
// the requester who filed it -- so those writes are part of the port.
type Store interface {
	Checker

	// Write records relationship facts. It is idempotent: writing a fact that is
	// already recorded is not an error, because the caller may be retrying.
	Write(ctx context.Context, tuples []Tuple) error
}

// Require turns a check into the error the service returns, so no call site has
// to remember what a false means.
func Require(ctx context.Context, checker Checker, principal string, action Action,
	object string) error {
	allowed, err := checker.Allowed(ctx, principal, action, object)
	if err != nil {
		return fmt.Errorf("authorization check %s on %s: %w", action, object, err)
	}
	if !allowed {
		return fmt.Errorf("%w: %s may not %s %s", ErrDenied, principal, action, object)
	}
	return nil
}

// splitObject separates an object's type from its identifier.
func splitObject(object string) (objectType, id string, ok bool) {
	objectType, id, ok = strings.Cut(object, ":")
	if !ok || objectType == "" || id == "" {
		return "", "", false
	}
	return objectType, id, true
}
