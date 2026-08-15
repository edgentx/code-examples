package authz

import (
	"context"
	"fmt"
	"sync"
)

// Memory is the in-memory adapter: the same relationships, resolved in process.
// It exists so the service's own tests, the acceptance criteria, and the console
// can run with no authorization server anywhere, and it is a real evaluator
// rather than a fake that returns a canned answer -- it walks tuples exactly as
// the model says to.
//
// What keeps it in step with the model is not care: it is ../authztest, the
// contract suite that both this adapter and the OpenFGA adapter run. If someone
// edits model.fga and not this file, the shared suite fails on whichever adapter
// was left behind.
type Memory struct {
	mu     sync.RWMutex
	tuples map[key]struct{}
}

// key is a tuple without its explanatory text, which is what identity is.
type key struct {
	user     string
	relation string
	object   string
}

// NewMemory returns an empty relationship store.
func NewMemory() *Memory {
	return &Memory{tuples: make(map[key]struct{})}
}

// Write records relationship facts.
func (m *Memory) Write(_ context.Context, tuples []Tuple) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, tuple := range tuples {
		if tuple.User == "" || tuple.Relation == "" || tuple.Object == "" {
			return fmt.Errorf("incomplete tuple %+v", tuple)
		}
		m.tuples[key{tuple.User, tuple.Relation, tuple.Object}] = struct{}{}
	}
	return nil
}

// Allowed resolves one relation.
//
// The switch is the model, written a second time in Go. That duplication is
// deliberate and it is bounded: every branch is one line, every branch is
// checked against the server by the shared contract suite, and the alternative
// -- shipping an authorization server with a console example so a reviewer can
// open it -- costs more than it teaches.
func (m *Memory) Allowed(_ context.Context, principal string, action Action,
	object string) (bool, error) {
	objectType, _, ok := splitObject(object)
	if !ok {
		return false, fmt.Errorf("malformed object %q", object)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	switch {
	case action == ActionSubmit && objectType == "office":
		return m.member(principal, object), nil
	case objectType != "records_request":
		return false, nil
	}

	office, known := m.officeOf(object)
	if !known {
		// A request nobody has related to an office is reachable by nobody. A
		// missing relationship is a deny, never a default allow.
		return false, nil
	}

	switch action {
	case ActionRead:
		return m.direct(principal, "requester", object) || m.member(principal, office), nil
	case ActionAcknowledge:
		return m.member(principal, office), nil
	case ActionAssignReviewer, ActionRelease, ActionDeny:
		return m.direct(principal, "reviewer", office), nil
	case ActionRecordDelivery:
		return m.direct(principal, "fulfillment", office), nil
	default:
		return false, nil
	}
}

// Permitted lists the actions a principal may take on a request.
func (m *Memory) Permitted(ctx context.Context, principal, object string) ([]Action, error) {
	return PermittedVia(ctx, m, principal, object)
}

// member is the office's `member` relation: clerk or reviewer.
func (m *Memory) member(principal, office string) bool {
	return m.direct(principal, "clerk", office) || m.direct(principal, "reviewer", office)
}

// officeOf finds the office a request belongs to.
func (m *Memory) officeOf(request string) (string, bool) {
	for stored := range m.tuples {
		if stored.relation == "office" && stored.object == request {
			return stored.user, true
		}
	}
	return "", false
}

// direct reports whether a tuple was written verbatim.
func (m *Memory) direct(user, relation, object string) bool {
	_, found := m.tuples[key{user, relation, object}]
	return found
}

// PermittedVia asks one check per action. It is written once here and shared by
// both adapters, so the list a console renders cannot drift from the checks the
// service enforces: every entry in the list is the answer to the same question
// the service will ask when the operator presses the button.
func PermittedVia(ctx context.Context, checker Checker, principal, object string) ([]Action, error) {
	allowed := make([]Action, 0, len(RequestActions))
	for _, action := range RequestActions {
		ok, err := checker.Allowed(ctx, principal, action, object)
		if err != nil {
			return nil, err
		}
		if ok {
			allowed = append(allowed, action)
		}
	}
	return allowed, nil
}
