// Package register holds the application service: the driving port. Its methods
// are the use cases an agency actually asks for -- issue a permit, suspend one,
// produce the renewal notice list -- expressed as a sequence of domain calls and
// repository calls and nothing else.
//
// The service depends on the permit.Repository interface, never on an adapter,
// so its tests run against the in-memory twin with no database present. A
// transport (HTTP handler, message consumer, CLI) is the driving adapter that
// calls these methods; there is no transport in this example because the point
// is the boundary, not the protocol.
package register

import (
	"context"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/permit"
)

// Clock is the port for reading the current time. Time is an outside dependency
// like any other, and injecting it is what makes "expiring in the next 30 days"
// a deterministic assertion instead of a test that fails at midnight.
type Clock func() time.Time

// Service is the application service. It is constructed with its ports and
// holds no state of its own.
type Service struct {
	permits permit.Repository
	now     Clock
}

// New builds a Service. Passing nil for the clock uses the wall clock.
func New(permits permit.Repository, now Clock) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{permits: permits, now: now}
}

// IssueCommand is the input to the issue use case.
type IssueCommand struct {
	Number string
	Holder string
	Kind   permit.Kind
	Site   string
}

// Issue validates an application and puts a new permit on the register. It
// returns permit.ErrDuplicateNumber unchanged when the number is taken, because
// the caller's decision -- reject the application as a duplicate -- is the same
// whichever adapter noticed.
func (s *Service) Issue(ctx context.Context, cmd IssueCommand) (permit.Permit, error) {
	issued, err := permit.Issue(cmd.Number, cmd.Holder, cmd.Kind, cmd.Site, s.now())
	if err != nil {
		return permit.Permit{}, err
	}
	return s.permits.Register(ctx, issued)
}

// Suspend stops work under a permit. It is the read-modify-write shape every
// use case of this kind has: load at a version, apply a domain rule, write back
// at that same version. If another officer wrote in between, the repository
// returns permit.ErrVersionConflict and this use case has changed nothing.
func (s *Service) Suspend(ctx context.Context, number string) (permit.Permit, error) {
	current, err := s.permits.ByNumber(ctx, number)
	if err != nil {
		return permit.Permit{}, err
	}
	suspended, err := current.Suspend()
	if err != nil {
		return permit.Permit{}, err
	}
	return s.permits.Update(ctx, suspended)
}

// Renew starts a fresh term on an active permit.
func (s *Service) Renew(ctx context.Context, number string) (permit.Permit, error) {
	current, err := s.permits.ByNumber(ctx, number)
	if err != nil {
		return permit.Permit{}, err
	}
	renewed, err := current.Renew(s.now())
	if err != nil {
		return permit.Permit{}, err
	}
	return s.permits.Update(ctx, renewed)
}

// RenewalNotices returns the permits a notice run should write to, in the order
// the run should work them: soonest expiry first. The window is expressed
// forward from the injected clock.
func (s *Service) RenewalNotices(ctx context.Context, within time.Duration) ([]permit.Permit, error) {
	if within <= 0 {
		return nil, permit.ErrMissingField
	}
	return s.permits.ExpiringBefore(ctx, s.now().Add(within))
}
