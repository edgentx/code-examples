package permit

import "errors"

// Domain rule violations. They are sentinel errors so a caller -- an HTTP
// handler choosing a status code, a test asserting a rule -- branches on the
// rule that was broken rather than on a message string.
var (
	// ErrMissingField is returned when a required value is absent or blank.
	ErrMissingField = errors.New("permit: required field is missing")
	// ErrUnknownKind is returned for a permit class the register does not issue.
	ErrUnknownKind = errors.New("permit: unknown permit kind")
	// ErrNotActive is returned when a transition requires an active permit.
	ErrNotActive = errors.New("permit: permit is not active")
	// ErrNotSuspended is returned when reinstating a permit that is not
	// suspended.
	ErrNotSuspended = errors.New("permit: permit is not suspended")
)

// Repository failures. These belong to the port, not to any one adapter: the
// SQLite adapter translates a UNIQUE constraint violation into
// ErrDuplicateNumber and the in-memory adapter reaches the same conclusion by
// checking a map, and no caller can tell which one it is talking to.
var (
	// ErrNotFound is returned when a number is not on the register.
	ErrNotFound = errors.New("permit: not found")
	// ErrDuplicateNumber is returned when registering a number already in use.
	ErrDuplicateNumber = errors.New("permit: permit number already registered")
	// ErrVersionConflict is returned when an update carries a version other than
	// the stored one, which means somebody else wrote first and the caller's copy
	// is stale.
	ErrVersionConflict = errors.New("permit: version conflict")
)
