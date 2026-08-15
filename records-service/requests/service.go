// Package requests is the application service: the thin layer between a caller
// and the domain, and the only place that knows how the guarantees are
// assembled.
//
// Every command follows the same six steps, in this order, and the order is the
// design:
//
//  1. Ask whether the caller may do this. A denial stops here and reads nothing.
//  2. Look up the idempotency key. A key that has been used returns the result
//     it produced the first time, without applying the command again.
//  3. Load the request from its events.
//  4. Compare the version the caller decided against with the version on the
//     record. A mismatch is a conflict the caller is told about, not an
//     overwrite.
//  5. Run the command. The domain accepts it and raises an event, or refuses it
//     and nothing happens.
//  6. Commit the events and their messages in one transaction, at the version
//     that was loaded.
//
// Step 2 and step 6 are both needed and they do different jobs. Step 2 keeps a
// resubmitted command from being refused by an invariant it already satisfied --
// a second acknowledgment is not "already acknowledged", it is the same
// acknowledgment. Step 6 is the guarantee: it is inside the transaction, so two
// simultaneous retries cannot both apply.
package requests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/projector"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// AnyVersion tells the service the caller did not read a version and is not
// asserting one. Interactive callers always assert one; a consumer reacting to a
// fact it just received has nothing to assert.
const AnyVersion = -1

// ErrUnknownAction is returned when a command arrives for an action the service
// does not have. It exists so a caller cannot reach the domain through a typo.
var ErrUnknownAction = errors.New("unknown action")

// Command is what every command carries regardless of what it does.
type Command struct {
	// Principal is who is asking, in the authorization model's vocabulary.
	Principal string
	// IdempotencyKey makes a retry harmless. It is required: a command without
	// one cannot be retried safely, and the service will not pretend otherwise.
	IdempotencyKey string
	// ExpectedVersion is the version the caller read before deciding. Use
	// AnyVersion when there was nothing to read.
	ExpectedVersion int
	// Trace is the trace to continue. Every message this command produces
	// carries it, so the work another service does in response lands in the same
	// trace as the request that caused it.
	Trace cloudevent.SpanContext
}

// Service is the records service.
type Service struct {
	repo    recordsrequest.Repository
	model   recordsrequest.Projection
	catchUp *projector.Projector
	access  authz.Store
	office  string
	now     func() time.Time
	newID   func() string
}

// Option adjusts a Service. The defaults are the ones a running service uses;
// tests replace the clock and the identifier generator so their assertions do
// not depend on when they ran.
type Option func(*Service)

// WithClock replaces the source of domain time.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithIDs replaces the request identifier generator.
func WithIDs(newID func() string) Option {
	return func(s *Service) { s.newID = newID }
}

// New wires a service to an event store, the read model built from it, an
// authorization store, and the office whose requests it handles.
func New(repo recordsrequest.Repository, model recordsrequest.Projection, access authz.Store,
	office string, options ...Option) *Service {
	service := &Service{
		repo:    repo,
		model:   model,
		catchUp: projector.New(repo, model, 0, nil),
		access:  access,
		office:  office,
		now:     func() time.Time { return time.Now().UTC() },
		newID:   generateID,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Office is the office this service files requests with.
func (s *Service) Office() string { return s.office }

// Submission is what filing a request needs.
type Submission struct {
	// RequesterPrincipal is the portal account the request belongs to, if there
	// is one. A walk-in filed at the counter has a name and no account, and
	// leaving this empty says exactly that rather than pretending the clerk who
	// typed it in is the requester.
	RequesterPrincipal string
	// Requester is the name on the request.
	Requester string
	// Description is the records being asked for.
	Description string
}

// Submit files a new request.
//
// The authorization facts are written before the events, not after. A crash
// between the two leaves relationship facts for a request that does not exist,
// which grants access to nothing; the other order would leave a request nobody
// -- including the person who filed it -- could read.
func (s *Service) Submit(ctx context.Context, cmd Command, in Submission) (View, error) {
	if err := authz.Require(ctx, s.access, cmd.Principal, authz.ActionSubmit,
		authz.OfficeObject(s.office)); err != nil {
		return View{}, err
	}
	if cmd.IdempotencyKey == "" {
		return View{}, recordsrequest.ErrNoIdempotencyKey
	}
	if recorded, found, err := s.repo.Recorded(ctx, cmd.IdempotencyKey); err != nil {
		return View{}, err
	} else if found {
		return s.View(ctx, cmd.Principal, recorded.RequestID)
	}

	requestID := s.newID()
	grants := []authz.Tuple{{
		User:     authz.OfficeObject(s.office),
		Relation: "office",
		Object:   authz.RequestObject(requestID),
		Why:      "the request was filed with this office",
	}}
	if in.RequesterPrincipal != "" {
		grants = append(grants, authz.Tuple{
			User:     in.RequesterPrincipal,
			Relation: "requester",
			Object:   authz.RequestObject(requestID),
			Why:      "the account the request was filed under",
		})
	}
	if err := s.access.Write(ctx, grants); err != nil {
		return View{}, fmt.Errorf("recording who may reach %s: %w", requestID, err)
	}

	request := recordsrequest.New()
	if err := request.Submit(recordsrequest.Submit{
		RequestID:   requestID,
		Requester:   in.Requester,
		Description: in.Description,
		At:          s.now(),
	}); err != nil {
		return View{}, err
	}
	return s.commit(ctx, cmd, request)
}

// Acknowledge sends the statutory receipt notice.
func (s *Service) Acknowledge(ctx context.Context, cmd Command, requestID string) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionAcknowledge,
		func(request *recordsrequest.Request) error {
			return request.Acknowledge(recordsrequest.Acknowledge{At: s.now()})
		})
}

// AssignReviewer routes the request to a records officer.
func (s *Service) AssignReviewer(ctx context.Context, cmd Command, requestID,
	reviewer string) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionAssignReviewer,
		func(request *recordsrequest.Request) error {
			return request.AssignReviewer(recordsrequest.AssignReviewer{
				Reviewer: reviewer,
				At:       s.now(),
			})
		})
}

// Release releases responsive records for delivery.
func (s *Service) Release(ctx context.Context, cmd Command, requestID string,
	pages int) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionRelease,
		func(request *recordsrequest.Request) error {
			return request.Fulfill(recordsrequest.Fulfill{ReleasedPages: pages, At: s.now()})
		})
}

// Deny withholds records under a cited exemption.
func (s *Service) Deny(ctx context.Context, cmd Command, requestID,
	exemption string) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionDeny,
		func(request *recordsrequest.Request) error {
			return request.Deny(recordsrequest.Deny{Exemption: exemption, At: s.now()})
		})
}

// ConfirmDelivery closes a request whose release package reached the requester.
func (s *Service) ConfirmDelivery(ctx context.Context, cmd Command, requestID,
	packageID string) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionRecordDelivery,
		func(request *recordsrequest.Request) error {
			return request.ConfirmDelivery(recordsrequest.ConfirmDelivery{
				PackageID: packageID,
				At:        s.now(),
			})
		})
}

// FailDelivery compensates a release that could not be delivered.
func (s *Service) FailDelivery(ctx context.Context, cmd Command, requestID,
	reason string) (View, error) {
	return s.apply(ctx, cmd, requestID, authz.ActionRecordDelivery,
		func(request *recordsrequest.Request) error {
			return request.FailDelivery(recordsrequest.FailDelivery{
				Reason: reason,
				At:     s.now(),
			})
		})
}

// View returns one request as the caller is allowed to see it.
func (s *Service) View(ctx context.Context, principal, requestID string) (View, error) {
	if err := authz.Require(ctx, s.access, principal, authz.ActionRead,
		authz.RequestObject(requestID)); err != nil {
		return View{}, err
	}
	request, err := s.repo.Load(ctx, requestID)
	if err != nil {
		return View{}, err
	}
	return s.viewOf(ctx, principal, request)
}

// List returns the requests the caller may read, in submission order.
//
// It reads the projection rather than replaying every stream: a list view that
// rehydrates each aggregate to render a row is the reason event-sourced systems
// get a reputation for slow lists, and the read model exists precisely so it
// does not have to. The projector is caught up first so an operator never files
// a request and then fails to see it, which is the one place a stale read model
// would be noticed immediately.
//
// Requests the caller may not read are absent rather than redacted: a list that
// shows identifiers a caller cannot open tells them what exists, which is itself
// a disclosure.
func (s *Service) List(ctx context.Context, principal string) ([]View, error) {
	if _, err := s.catchUp.CatchUp(ctx); err != nil {
		return nil, err
	}
	summaries, err := s.model.Summaries(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(summaries))
	for _, summary := range summaries {
		allowed, err := s.access.Allowed(ctx, principal, authz.ActionRead,
			authz.RequestObject(summary.RequestID))
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		actions, err := s.access.Permitted(ctx, principal,
			authz.RequestObject(summary.RequestID))
		if err != nil {
			return nil, err
		}
		views = append(views, viewOfSummary(summary, actions))
	}
	return views, nil
}

// apply is the six steps, written once.
func (s *Service) apply(ctx context.Context, cmd Command, requestID string,
	action authz.Action, mutate func(*recordsrequest.Request) error) (View, error) {
	if err := authz.Require(ctx, s.access, cmd.Principal, action,
		authz.RequestObject(requestID)); err != nil {
		return View{}, err
	}
	if cmd.IdempotencyKey == "" {
		return View{}, recordsrequest.ErrNoIdempotencyKey
	}
	if _, found, err := s.repo.Recorded(ctx, cmd.IdempotencyKey); err != nil {
		return View{}, err
	} else if found {
		// The command already ran. Returning the current view rather than
		// rerunning it is what makes a retry safe for a command the domain would
		// now refuse: acknowledging twice is one acknowledgment, not an error.
		return s.View(ctx, cmd.Principal, requestID)
	}

	request, err := s.repo.Load(ctx, requestID)
	if err != nil {
		return View{}, err
	}
	if cmd.ExpectedVersion != AnyVersion && cmd.ExpectedVersion != request.Version() {
		// The caller decided against a version that is no longer current. This
		// is the same conflict the store would raise, caught early so the caller
		// is told before anything is written.
		return View{}, recordsrequest.ErrVersionConflict
	}
	if err := mutate(request); err != nil {
		return View{}, err
	}
	return s.commit(ctx, cmd, request)
}

// commit appends the raised events to the stream, stamped with the metadata the
// relay and an auditor both need.
func (s *Service) commit(ctx context.Context, cmd Command,
	request *recordsrequest.Request) (View, error) {
	result, err := s.repo.Append(ctx, recordsrequest.Append{
		RequestID:       request.ID(),
		ExpectedVersion: request.CommittedVersion(),
		IdempotencyKey:  cmd.IdempotencyKey,
		TraceParent:     cmd.Trace.TraceParent(),
		Events:          request.PendingEvents(),
	})
	if err != nil {
		return View{}, err
	}
	if result.Replayed {
		// Another copy of this command won the race inside the transaction.
		return s.View(ctx, cmd.Principal, result.RequestID)
	}
	request.MarkCommitted()
	return s.viewOf(ctx, cmd.Principal, request)
}

// viewOf renders a request together with what this caller may do to it.
func (s *Service) viewOf(ctx context.Context, principal string,
	request *recordsrequest.Request) (View, error) {
	actions, err := s.access.Permitted(ctx, principal, authz.RequestObject(request.ID()))
	if err != nil {
		return View{}, err
	}
	return newView(request, actions), nil
}

// generateID mints a request identifier. The random suffix keeps two intake
// desks from minting the same identifier without either of them consulting a
// shared counter.
func generateID() string {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		// The identifier generator has no error path a caller could act on, and
		// a records service that cannot mint an identifier must not carry on
		// with a predictable one.
		panic(fmt.Sprintf("generating a request identifier: %v", err))
	}
	return fmt.Sprintf("PRR-%d-%s", time.Now().UTC().Year(),
		hex.EncodeToString(suffix))
}
