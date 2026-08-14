package intake

import "errors"

// Sentinel errors so callers branch on the failure, not on a message string.
// The subscriber in particular has to turn each of these into a specific
// answer on the wire, and picking the wrong answer is how messages get lost.
var (
	// ErrMissingField is returned when an intake notice omits a value the
	// publishing service requires before it will put anything on the topic.
	ErrMissingField = errors.New("required field is missing")
	// ErrNotPositive is returned when a notice reports an impossible page count.
	ErrNotPositive = errors.New("page count must be greater than zero")
	// ErrBadEnvelope is returned when a delivered body is not a usable
	// CloudEvent: unparseable, or missing an attribute the router needs.
	ErrBadEnvelope = errors.New("cloud event envelope is malformed")
	// ErrUnknownSeries is returned when a notice cites a record series code
	// that is not in the retention catalog. This is the example's processing
	// failure: it is deterministic, so the poison-message test is deterministic.
	ErrUnknownSeries = errors.New("record series code is not in the retention catalog")
	// ErrETagConflict is returned when a conditional state write lost the race
	// to another writer. The value on the server is newer than the one read.
	ErrETagConflict = errors.New("state was modified concurrently")
	// ErrStateFailed is returned when the state store could not be read or
	// written at all, which is different from losing an etag race.
	ErrStateFailed = errors.New("state store operation failed")
	// ErrPublishRejected is returned when the sidecar refused a publish.
	ErrPublishRejected = errors.New("sidecar rejected the publish")
)
