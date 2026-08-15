package recordsrequest

import (
	"context"
	"sort"
	"time"
)

// Repository is the driven port: an event store, in the domain's own words.
//
// There is no row anywhere holding the current state of a request. The stream of
// events is the record, and everything else in the service is a consumer of it:
//
//	rehydration -> Load replays a stream into an aggregate
//	projection  -> Stream feeds the read model the console lists from
//	outbox      -> PendingOutbox feeds the relay that announces facts to
//	               other services
//
// Because all three read the same stream, they cannot disagree about what
// happened; a read model can be deleted and rebuilt, and a message can be
// rebuilt from the event it announces rather than from a second copy of it.
//
// Three guarantees the domain cannot provide for itself are stated here, and
// every implementation owes all three. They are not prose promises: they are
// written as an executable contract in ../storetest, which both adapters run.
//
//   - Optimistic locking. An append is accepted only at ExpectedVersion, and
//     the store enforces it with a uniqueness constraint on (stream, version)
//     rather than with a check a future refactor could move outside the
//     transaction. A writer that read an older version is rejected with
//     ErrVersionConflict and writes nothing at all.
//   - Idempotency. An append carries the key of the command that caused it. The
//     first append under a key is applied; every later one is a replay that
//     appends nothing and returns the original result.
//   - Transactional outbox. The events and the outbox entries that make them
//     publishable are written in the same transaction. Either both land or
//     neither does, so there is no window in which a fact is recorded and its
//     announcement is lost, and none in which an announcement escapes for a
//     fact that was rolled back.
type Repository interface {
	// Load rebuilds a request by replaying its stream. It returns ErrNotFound if
	// no events have ever been written for the identifier.
	Load(ctx context.Context, requestID string) (*Request, error)

	// Recorded reports the result of an earlier append under an idempotency key.
	// It is the cheap pre-check a command handler makes before it applies a
	// command, so that a resubmitted command returns the original result rather
	// than being rejected by an invariant it already satisfied. It is a hint,
	// not the guarantee: the authoritative check happens inside Append.
	Recorded(ctx context.Context, idempotencyKey string) (Result, bool, error)

	// Append writes the events of one command to a stream, at the version they
	// were decided against, together with their outbox entries. It returns
	// ErrVersionConflict if the stream has moved past ExpectedVersion, and a
	// replayed Result if the idempotency key has been seen before.
	Append(ctx context.Context, append Append) (Result, error)

	// Stream reads the whole store's events in the order they were committed,
	// after a sequence number, at most limit at a time. It is the catch-up read
	// a projection uses; the sequence it returns is the projection's checkpoint.
	Stream(ctx context.Context, afterSequence int64, limit int) ([]Stored, error)

	// PendingOutbox returns committed events that have not been published,
	// oldest first, at most limit. The events themselves come back, not copies
	// of them: the message the relay publishes is built from the record.
	PendingOutbox(ctx context.Context, limit int) ([]Stored, error)

	// MarkDispatched records that an event's message was handed to the broker.
	// It is called after the publish returns, never before: a relay that marks
	// first and publishes second loses the message when the process dies between
	// the two, which is the failure the outbox exists to prevent.
	MarkDispatched(ctx context.Context, sequence int64) error
}

// Append is one atomic unit of change: the facts, the version they were decided
// against, and the metadata every one of them is stamped with.
type Append struct {
	RequestID       string
	ExpectedVersion int
	// IdempotencyKey makes replaying the command harmless. It is stamped onto
	// every event of the append as well as recorded, so an auditor reading the
	// stream can see which command produced which facts.
	IdempotencyKey string
	// TraceParent is the W3C trace context of the command. Recording it with the
	// event is what lets a message published minutes later still belong to the
	// trace of the request that caused it.
	TraceParent string
	Events      []Event
}

// Result is what an append produced, or what the first append under the same key
// produced if this one was a replay.
type Result struct {
	RequestID string
	// Version is the request's version after the append.
	Version int
	// Replayed is true when the idempotency key had already been recorded and
	// nothing was appended.
	Replayed bool
}

// Metadata is what is recorded about a fact besides the fact itself.
type Metadata struct {
	TraceParent    string `json:"traceparent,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Stored is one event as it sits in the store.
type Stored struct {
	// Sequence is the store-wide position, assigned on commit and never reused.
	// It is monotonic across every stream, which is what makes it usable as a
	// projection's checkpoint.
	Sequence int64
	// RequestID is the stream the event belongs to.
	RequestID string
	// Version is the event's position within its own stream, starting at 1. The
	// pair (RequestID, Version) is unique, and that constraint is the lock.
	Version    int
	Name       string
	Payload    []byte
	Metadata   Metadata
	RecordedAt time.Time
}

// Event decodes the stored event back into a domain fact.
func (s Stored) Event() (Event, error) { return DecodeEvent(s.Name, s.Payload) }

// Projection is the read model port: the list the console shows, kept up to date
// by replaying the same stream the aggregates are rebuilt from.
//
// A projection is disposable by construction. Nothing is written here that is
// not derivable from the events, so a broken read model is fixed by deleting it
// and letting the projector catch up from sequence zero.
type Projection interface {
	// Checkpoint is the sequence the projection has consumed through.
	Checkpoint(ctx context.Context) (int64, error)

	// Save writes summaries and advances the checkpoint in one transaction.
	// It is idempotent by sequence: a batch that ends at or before the current
	// checkpoint has already been applied and is ignored, so a projector that
	// crashed after saving and before recording its progress does no harm when
	// it repeats the batch.
	Save(ctx context.Context, summaries []Summary, throughSequence int64) error

	// Summaries returns every request the projection knows about, in submission
	// order.
	Summaries(ctx context.Context) ([]Summary, error)
}

// Summary is one row of the read model: everything the console's list needs and
// nothing it does not.
type Summary struct {
	RequestID     string
	Requester     string
	Description   string
	Status        Status
	Reviewer      string
	SubmittedAt   time.Time
	DueAt         time.Time
	ReleasedPages int
	Exemption     string
	PackageID     string
	FailureCause  string
	Version       int
}

// SummaryOf renders a rehydrated aggregate as a read-model row.
//
// The projection is derived from the aggregate rather than folded a second time
// from the events. Two folds over one stream is two places for the same rule,
// and they drift; loading the stream and rendering it is one rule, and at the
// size of a records office it is also faster than the alternative is worth.
func SummaryOf(request *Request) Summary {
	return Summary{
		RequestID:     request.ID(),
		Requester:     request.Requester(),
		Description:   request.Description(),
		Status:        request.Status(),
		Reviewer:      request.Reviewer(),
		SubmittedAt:   request.SubmittedAt(),
		DueAt:         request.DueAt(),
		ReleasedPages: request.ReleasedPages(),
		Exemption:     request.Exemption(),
		PackageID:     request.PackageID(),
		FailureCause:  request.FailureCause(),
		Version:       request.Version(),
	}
}

// SortSummaries puts summaries in submission order, ties broken by identifier so
// that two implementations of the port return the same list in the same order.
func SortSummaries(summaries []Summary) {
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].SubmittedAt.Equal(summaries[j].SubmittedAt) {
			return summaries[i].RequestID < summaries[j].RequestID
		}
		return summaries[i].SubmittedAt.Before(summaries[j].SubmittedAt)
	})
}
