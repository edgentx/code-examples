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
	// ErrClosed is returned when any command arrives after fulfillment or denial.
	ErrClosed = errors.New("records request is closed")
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
