// Package recordsrequest models a public records request as an event-sourced
// aggregate: commands are validated against current state, accepted commands
// produce events, and state is only ever changed by applying an event.
//
// The shape is deliberately small enough to read in one sitting and is the same
// shape used for larger aggregates: command method -> invariant guard -> raise
// event -> apply. Everything a reviewer needs in order to trust the model is in
// the guards, and everything the model knows can be rebuilt by replay.
package recordsrequest

import (
	"strings"
	"time"
)

// responseWindow is the statutory period an agency has to respond. It is fixed
// at submission time so that a later change to the statute cannot silently
// re-date requests that are already open.
const responseWindow = 10 * 24 * time.Hour

// Status is the request lifecycle state derived from the event history.
type Status string

const (
	// StatusNew is the zero value: no Submitted event has been applied.
	StatusNew Status = "new"
	// StatusOpen means the request exists but no receipt notice has gone out.
	StatusOpen Status = "open"
	// StatusAcknowledged means the requester has the statutory receipt notice.
	StatusAcknowledged Status = "acknowledged"
	// StatusFulfilled means responsive records were released.
	StatusFulfilled Status = "fulfilled"
	// StatusDenied means records were withheld under a cited exemption.
	StatusDenied Status = "denied"
)

// Request is the aggregate. Its fields are unexported: the only way to change a
// Request is to issue a command, and the only way to read one is through the
// accessors, so no caller can put it into a state the invariants forbid.
type Request struct {
	id          string
	requester   string
	description string
	status      Status
	reviewer    string
	dueAt       time.Time
	pages       int
	exemption   string

	version int
	pending []Event
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
	return request, nil
}

// ID returns the request identifier, empty until the request is submitted.
func (r *Request) ID() string { return r.id }

// Status returns the lifecycle state derived from the applied events.
func (r *Request) Status() Status { return r.status }

// Reviewer returns the assigned records officer, empty if none is assigned.
func (r *Request) Reviewer() string { return r.reviewer }

// DueAt returns the statutory response deadline fixed at submission.
func (r *Request) DueAt() time.Time { return r.dueAt }

// ReleasedPages returns the page count reported at fulfillment.
func (r *Request) ReleasedPages() int { return r.pages }

// Exemption returns the citation recorded at denial, empty if not denied.
func (r *Request) Exemption() string { return r.exemption }

// Version is the number of events applied, used for optimistic concurrency when
// appending to the event store.
func (r *Request) Version() int { return r.version }

// PendingEvents returns the events raised since the aggregate was loaded. The
// caller appends them to the store and then calls MarkCommitted.
func (r *Request) PendingEvents() []Event {
	events := make([]Event, len(r.pending))
	copy(events, r.pending)
	return events
}

// MarkCommitted clears the pending events after a successful append.
func (r *Request) MarkCommitted() { r.pending = nil }

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
	if err := r.requireOpen(); err != nil {
		return err
	}
	if r.status == StatusAcknowledged {
		return ErrAlreadyAcknowledged
	}
	return r.raise(Acknowledged{RequestID: r.id, At: cmd.At})
}

// AssignReviewer routes an acknowledged request to a records officer. Requests
// may be reassigned, but assigning the current reviewer again is rejected so the
// history does not fill with events that changed nothing.
func (r *Request) AssignReviewer(cmd AssignReviewer) error {
	if err := r.requireOpen(); err != nil {
		return err
	}
	if r.status != StatusAcknowledged {
		return ErrNotAcknowledged
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

// Fulfill closes the request by releasing responsive records.
func (r *Request) Fulfill(cmd Fulfill) error {
	if err := r.requireOpen(); err != nil {
		return err
	}
	if r.status != StatusAcknowledged {
		return ErrNotAcknowledged
	}
	if r.reviewer == "" {
		return ErrNoReviewer
	}
	if cmd.ReleasedPages < 0 {
		return ErrNegativePages
	}
	return r.raise(Fulfilled{RequestID: r.id, ReleasedPages: cmd.ReleasedPages, At: cmd.At})
}

// Deny closes the request by withholding records. The exemption citation is
// required: a denial an agency cannot justify in writing is not a denial it can
// defend on appeal.
func (r *Request) Deny(cmd Deny) error {
	if err := r.requireOpen(); err != nil {
		return err
	}
	if r.status != StatusAcknowledged {
		return ErrNotAcknowledged
	}
	if r.reviewer == "" {
		return ErrNoReviewer
	}
	if strings.TrimSpace(cmd.Exemption) == "" {
		return ErrMissingField
	}
	return r.raise(Denied{RequestID: r.id, Exemption: cmd.Exemption, At: cmd.At})
}

// requireOpen rejects commands for requests that do not exist or are closed.
func (r *Request) requireOpen() error {
	switch r.status {
	case StatusNew:
		return ErrNotSubmitted
	case StatusFulfilled, StatusDenied:
		return ErrClosed
	default:
		return nil
	}
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
		r.dueAt = e.DueAt
		r.status = StatusOpen
	case Acknowledged:
		r.status = StatusAcknowledged
	case ReviewerAssigned:
		r.reviewer = e.Reviewer
	case Fulfilled:
		r.pages = e.ReleasedPages
		r.status = StatusFulfilled
	case Denied:
		r.exemption = e.Exemption
		r.status = StatusDenied
	default:
		return ErrUnknownEvent
	}
	return nil
}
