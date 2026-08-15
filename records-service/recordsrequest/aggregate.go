// Package recordsrequest models a public records request as an event-sourced
// aggregate: commands are validated against current state, accepted commands
// produce events, and state is only ever changed by applying an event.
//
// This is the domain core of the records service. It began as the aggregate in
// ../../event-sourced-aggregate and is carried here by copy so this directory
// stays self-contained; the extension is the release-and-delivery leg, which is
// what gives the service a transaction that spans two services. Releasing
// records no longer closes a request. It moves the request to release_pending,
// a second service assembles and delivers the package, and the request is only
// closed by the fact that comes back: a delivery confirmation, or a compensating
// release failure that withdraws the release and puts the request back in front
// of the officer.
//
// The aggregate holds no persistence, transport, or coordination code. It does
// not know that its events are written through an outbox, nor that another
// service reads them.
package recordsrequest

import (
	"strings"
	"time"
)

// responseWindow is the statutory period an agency has to respond. It is fixed
// at submission time so that a later change to the statute cannot silently
// re-date requests that are already open.
const responseWindow = 10 * 24 * time.Hour

// Status is the request lifecycle state derived from the event history. It is
// the single field every downstream reader -- the API, the console, the
// fulfillment service -- derives its own view from. Nothing keeps a parallel
// flag that could disagree with it.
type Status string

const (
	// StatusNew is the zero value: no Submitted event has been applied.
	StatusNew Status = "new"
	// StatusOpen means the request exists but no receipt notice has gone out.
	StatusOpen Status = "open"
	// StatusAcknowledged means the requester has the statutory receipt notice.
	StatusAcknowledged Status = "acknowledged"
	// StatusReleasePending means responsive records were released and the
	// package is out for assembly and delivery. The request is neither open for
	// officer action nor closed.
	StatusReleasePending Status = "release_pending"
	// StatusReleaseFailed means a release was compensated: delivery could not be
	// completed, the page count was withdrawn, and the request is workable again.
	StatusReleaseFailed Status = "release_failed"
	// StatusFulfilled means the release package reached the requester.
	StatusFulfilled Status = "fulfilled"
	// StatusDenied means records were withheld under a cited exemption.
	StatusDenied Status = "denied"
)

// Request is the aggregate. Its fields are unexported: the only way to change a
// Request is to issue a command, and the only way to read one is through the
// accessors, so no caller can put it into a state the invariants forbid.
type Request struct {
	id           string
	requester    string
	description  string
	status       Status
	reviewer     string
	submittedAt  time.Time
	dueAt        time.Time
	pages        int
	exemption    string
	packageID    string
	failureCause string

	version   int
	committed int
	pending   []Event
}

// New returns an empty aggregate ready to accept a Submit command.
func New() *Request {
	return &Request{status: StatusNew}
}

// FromHistory rebuilds an aggregate by replaying stored events in order. Replay
// applies events directly and never runs the command guards: history is fact,
// and a rule added today must not make yesterday's history unloadable.
func FromHistory(history []Event) (*Request, error) {
	request := New()
	for _, event := range history {
		if err := request.apply(event); err != nil {
			return nil, err
		}
		request.version++
	}
	request.committed = request.version
	return request, nil
}

// ID returns the request identifier, empty until the request is submitted.
func (r *Request) ID() string { return r.id }

// Requester returns the name on the request.
func (r *Request) Requester() string { return r.requester }

// Description returns the records described in the request.
func (r *Request) Description() string { return r.description }

// Status returns the lifecycle state derived from the applied events.
func (r *Request) Status() Status { return r.status }

// Reviewer returns the assigned records officer, empty if none is assigned.
func (r *Request) Reviewer() string { return r.reviewer }

// SubmittedAt returns the time the request was received.
func (r *Request) SubmittedAt() time.Time { return r.submittedAt }

// DueAt returns the statutory response deadline fixed at submission.
func (r *Request) DueAt() time.Time { return r.dueAt }

// ReleasedPages returns the page count currently released. A compensated
// release withdraws it back to zero.
func (r *Request) ReleasedPages() int { return r.pages }

// Exemption returns the citation recorded at denial, empty if not denied.
func (r *Request) Exemption() string { return r.exemption }

// PackageID returns the delivered package identifier, empty until delivery is
// confirmed.
func (r *Request) PackageID() string { return r.packageID }

// FailureCause returns why the last release could not be delivered, empty if no
// release has been compensated.
func (r *Request) FailureCause() string { return r.failureCause }

// Version is the number of events applied, used for optimistic concurrency when
// appending to the event store.
func (r *Request) Version() int { return r.version }

// CommittedVersion is the version the aggregate was loaded at, which is the
// version an append is conditioned on. It does not move when a command raises
// an event; it moves when the store accepts the append.
func (r *Request) CommittedVersion() int { return r.committed }

// PendingEvents returns the events raised since the aggregate was loaded. The
// caller appends them to the store and then calls MarkCommitted.
func (r *Request) PendingEvents() []Event {
	events := make([]Event, len(r.pending))
	copy(events, r.pending)
	return events
}

// MarkCommitted clears the pending events after a successful append.
func (r *Request) MarkCommitted() {
	r.pending = nil
	r.committed = r.version
}

// Submit opens the request and starts the statutory clock.
func (r *Request) Submit(cmd Submit) error {
	if r.status != StatusNew {
		return ErrAlreadySubmitted
	}
	if strings.TrimSpace(cmd.RequestID) == "" ||
		strings.TrimSpace(cmd.Requester) == "" ||
		strings.TrimSpace(cmd.Description) == "" {
		return ErrMissingField
	}
	return r.raise(Submitted{
		RequestID:   cmd.RequestID,
		Requester:   cmd.Requester,
		Description: cmd.Description,
		At:          cmd.At,
		DueAt:       cmd.At.Add(responseWindow),
	})
}

// Acknowledge records the receipt notice sent to the requester.
func (r *Request) Acknowledge(cmd Acknowledge) error {
	if err := r.requireWorkable(); err != nil {
		return err
	}
	if r.status != StatusOpen {
		return ErrAlreadyAcknowledged
	}
	return r.raise(Acknowledged{RequestID: r.id, At: cmd.At})
}

// AssignReviewer routes an acknowledged request to a records officer. Requests
// may be reassigned, but assigning the current reviewer again is rejected so the
// history does not fill with events that changed nothing.
func (r *Request) AssignReviewer(cmd AssignReviewer) error {
	if err := r.requireAcknowledged(); err != nil {
		return err
	}
	reviewer := strings.TrimSpace(cmd.Reviewer)
	if reviewer == "" {
		return ErrMissingField
	}
	if reviewer == r.reviewer {
		return ErrSameReviewer
	}
	return r.raise(ReviewerAssigned{RequestID: r.id, Reviewer: reviewer, At: cmd.At})
}

// Fulfill releases responsive records for delivery. A release that was
// compensated may be attempted again, which is the point of compensating rather
// than closing the request in a failed state.
func (r *Request) Fulfill(cmd Fulfill) error {
	if err := r.requireAcknowledged(); err != nil {
		return err
	}
	if r.reviewer == "" {
		return ErrNoReviewer
	}
	if cmd.ReleasedPages < 0 {
		return ErrNegativePages
	}
	return r.raise(Fulfilled{RequestID: r.id, ReleasedPages: cmd.ReleasedPages, At: cmd.At})
}

// ConfirmDelivery closes the request on the strength of the delivery fact
// reported by the fulfillment service.
func (r *Request) ConfirmDelivery(cmd ConfirmDelivery) error {
	if r.status != StatusReleasePending {
		return ErrNotAwaitingDelivery
	}
	if strings.TrimSpace(cmd.PackageID) == "" {
		return ErrMissingField
	}
	return r.raise(DeliveryConfirmed{RequestID: r.id, PackageID: cmd.PackageID, At: cmd.At})
}

// FailDelivery compensates a release that could not be delivered. The reason is
// required: an officer asked to work a request a second time is owed the reason
// the first attempt did not land.
func (r *Request) FailDelivery(cmd FailDelivery) error {
	if r.status != StatusReleasePending {
		return ErrNotAwaitingDelivery
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return ErrMissingField
	}
	return r.raise(ReleaseFailed{RequestID: r.id, Reason: cmd.Reason, At: cmd.At})
}

// Deny closes the request by withholding records. The exemption citation is
// required: a denial an agency cannot justify in writing is not a denial it can
// defend on appeal.
func (r *Request) Deny(cmd Deny) error {
	if err := r.requireAcknowledged(); err != nil {
		return err
	}
	if r.reviewer == "" {
		return ErrNoReviewer
	}
	if strings.TrimSpace(cmd.Exemption) == "" {
		return ErrMissingField
	}
	return r.raise(Denied{RequestID: r.id, Exemption: cmd.Exemption, At: cmd.At})
}

// requireWorkable rejects officer commands for requests that do not exist, are
// closed, or have a release whose outcome is not known yet.
func (r *Request) requireWorkable() error {
	switch r.status {
	case StatusNew:
		return ErrNotSubmitted
	case StatusFulfilled, StatusDenied:
		return ErrClosed
	case StatusReleasePending:
		return ErrReleaseInFlight
	default:
		return nil
	}
}

// requireAcknowledged additionally demands that the statutory receipt notice has
// gone out. A compensated release satisfies it: the notice went out long ago.
func (r *Request) requireAcknowledged() error {
	if err := r.requireWorkable(); err != nil {
		return err
	}
	if r.status != StatusAcknowledged && r.status != StatusReleaseFailed {
		return ErrNotAcknowledged
	}
	return nil
}

// raise applies an accepted event and queues it for the event store.
func (r *Request) raise(event Event) error {
	if err := r.apply(event); err != nil {
		return err
	}
	r.pending = append(r.pending, event)
	r.version++
	return nil
}

// apply is the single mutation point. It contains no rules: every guard lives in
// a command method, so replaying history can never be rejected by a rule that
// did not exist when the event was written.
func (r *Request) apply(event Event) error {
	switch e := event.(type) {
	case Submitted:
		r.id = e.RequestID
		r.requester = e.Requester
		r.description = e.Description
		r.submittedAt = e.At
		r.dueAt = e.DueAt
		r.status = StatusOpen
	case Acknowledged:
		r.status = StatusAcknowledged
	case ReviewerAssigned:
		r.reviewer = e.Reviewer
	case Fulfilled:
		r.pages = e.ReleasedPages
		r.failureCause = ""
		r.status = StatusReleasePending
	case DeliveryConfirmed:
		r.packageID = e.PackageID
		r.status = StatusFulfilled
	case ReleaseFailed:
		// The compensation withdraws the release rather than annotating it: the
		// page count went out with a package that never arrived, so leaving it
		// standing would tell an auditor records were released that were not.
		r.pages = 0
		r.failureCause = e.Reason
		r.status = StatusReleaseFailed
	case Denied:
		r.exemption = e.Exemption
		r.status = StatusDenied
	default:
		return ErrUnknownEvent
	}
	return nil
}
