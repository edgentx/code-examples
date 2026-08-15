package recordsrequest

import "errors"

// Invariant violations are sentinel errors so callers -- an HTTP handler mapping
// to a status code, a test asserting a rule -- can branch on the rule that was
// broken rather than on a message string.
var (
	// ErrNotSubmitted is returned when a command arrives for a request that does
	// not exist yet.
	ErrNotSubmitted = errors.New("records request has not been submitted")
	// ErrAlreadySubmitted is returned when a request identifier is reused.
	ErrAlreadySubmitted = errors.New("records request has already been submitted")
	// ErrAlreadyAcknowledged is returned when a second receipt notice is attempted.
	ErrAlreadyAcknowledged = errors.New("records request has already been acknowledged")
	// ErrNotAcknowledged is returned when work is attempted before the statutory
	// receipt notice has gone out.
	ErrNotAcknowledged = errors.New("records request has not been acknowledged")
	// ErrClosed is returned when any command arrives after the request is closed.
	ErrClosed = errors.New("records request is closed")
	// ErrReleaseInFlight is returned when an officer command arrives while a
	// release package is out for delivery. The outcome of that release is not
	// known yet, so acting on the request now would be acting on a guess.
	ErrReleaseInFlight = errors.New("records request has a release awaiting delivery")
	// ErrNotAwaitingDelivery is returned when a delivery outcome arrives for a
	// request that has no release in flight.
	ErrNotAwaitingDelivery = errors.New("records request is not awaiting delivery")
	// ErrNoReviewer is returned when a request would be answered with nobody
	// accountable for the response.
	ErrNoReviewer = errors.New("records request has no assigned reviewer")
	// ErrSameReviewer is returned when a reassignment names the current reviewer.
	ErrSameReviewer = errors.New("reviewer is already assigned to this request")
	// ErrMissingField is returned when a command omits a value the domain requires.
	ErrMissingField = errors.New("required field is missing")
	// ErrNegativePages is returned when a fulfillment reports an impossible count.
	ErrNegativePages = errors.New("released page count cannot be negative")
	// ErrUnknownEvent is returned when replay encounters an event the current code
	// does not understand, which means the history and the model have diverged.
	ErrUnknownEvent = errors.New("unknown event in history")
)

// Storage failures the domain has to be able to name, because a caller decides
// what to do about them: a conflict is a retry the operator has to be shown, a
// missing request is a 404, and neither is an unexpected error to be logged and
// swallowed.
var (
	// ErrNotFound is returned when no events have ever been written for a
	// request identifier.
	ErrNotFound = errors.New("records request not found")
	// ErrVersionConflict is returned when a write was decided against a version
	// the store has already moved past. Nothing was written.
	ErrVersionConflict = errors.New("records request was changed by another writer")
	// ErrNoIdempotencyKey is returned when a write arrives without the key that
	// makes replaying it harmless.
	ErrNoIdempotencyKey = errors.New("write has no idempotency key")
	// ErrNoChanges is returned when a write carries no facts. A command that
	// decided nothing must not consume an idempotency key, because the key would
	// then suppress the retry that was going to do the work.
	ErrNoChanges = errors.New("write carries no changes")
)
