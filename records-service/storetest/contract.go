// Package storetest holds the executable contract for the event store: one
// exported suite that any implementation of recordsrequest.Repository and
// recordsrequest.Projection must pass.
//
// This is the load-bearing part of the back end. An interface only says what the
// method names are. It cannot say that a stale append writes nothing, that a
// rejected append leaves no message queued for the broker, that a replayed
// idempotency key returns the original result, that eight writers racing at the
// same version produce exactly one event, or that an aggregate rebuilt from the
// stream is indistinguishable from the one that wrote it. Those are the promises
// the service is built on, so they are written down once, here, and every
// adapter runs them. The in-memory twin is then trustworthy as a test double for
// exactly one reason: it satisfies the same contract as the store that runs in
// production.
//
// The suite lives in a normal (non-_test) file so adapters in other packages can
// import it, which is the same arrangement the standard library uses for
// testing/fstest and net/http/httptest.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/edgentx/code-examples/records-service/outbox"
	"github.com/edgentx/code-examples/records-service/projector"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// Store is what one adapter provides: the stream of record, and the read model
// derived from it. They are separate ports because they are separate concerns,
// and one type implements both because they are one database.
type Store interface {
	recordsrequest.Repository
	recordsrequest.Projection
}

// day0 is the fixture epoch. Every fixture time is expressed relative to it so
// the assertions do not depend on when the suite runs.
var day0 = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

// traceParent is a fixed, well-formed W3C trace context, so a case can assert
// that the metadata recorded with an event is the metadata that was sent.
const traceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// racers is how many writers the concurrency cases run. It is more than two so
// that a store which happens to serialize a pair correctly still has to prove it
// under a crowd.
const racers = 8

// RunRepositoryContract exercises the whole event store against an
// implementation. newStore is called once per case and must return an empty
// store; it takes *testing.T so an adapter can register its own cleanup.
func RunRepositoryContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()

	tests := []struct {
		name string
		run  func(t *testing.T, store Store)
	}{
		{"an unknown request is not found", unknownRequestIsNotFound},
		{"an appended stream reloads as the aggregate that wrote it", appendReloads},
		{"a rehydrated aggregate is indistinguishable from the live one", replayEquivalence},
		{"every event carries the trace and the key of the command that caused it", metadataIsRecorded},
		{"an append without an idempotency key is refused", appendNeedsKey},
		{"an append with no events is refused", appendNeedsEvents},
		{"a stale append is rejected and writes nothing", staleAppendIsRejected},
		{"a rejected append queues no message for the broker", rejectedAppendQueuesNothing},
		{"events and their outbox entries are committed together", eventsAndEntriesCommitTogether},
		{"an unused idempotency key is not recorded", unusedKeyIsNotRecorded},
		{"a replayed idempotency key appends nothing", replayedKeyAppendsNothing},
		{"the stream reads back in commit order", streamReadsInCommitOrder},
		{"pending messages come back oldest first", pendingIsOrdered},
		{"a dispatched message stops being pending", dispatchedStopsBeingPending},
		{"marking a dispatched message again is not an error", markingTwiceIsNotAnError},
		{"the projection catches up and lists in submission order", projectionCatchesUp},
		{"the projection ignores a batch it has already applied", projectionIgnoresRepeats},
		{"the projection can be rebuilt from the stream alone", projectionRebuilds},
		{"interleaved writers produce exactly one event", interleavedWritersProduceOneEvent},
		{"a double submission produces exactly one event", doubleSubmissionProducesOneEvent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, newStore(t))
		})
	}
}

func unknownRequestIsNotFound(t *testing.T, store Store) {
	_, err := store.Load(context.Background(), "PRR-2026-9999")

	if !errors.Is(err, recordsrequest.ErrNotFound) {
		t.Fatalf("Load error = %v, want %v", err, recordsrequest.ErrNotFound)
	}
}

func appendReloads(t *testing.T, store Store) {
	ctx := context.Background()
	request, write := submission(t, "PRR-2026-0041", "key-submit", day0)

	result, err := store.Append(ctx, write)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if result.Version != 1 || result.Replayed {
		t.Fatalf("Result = %+v, want version 1 and no replay", result)
	}

	loaded, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Status() != request.Status() {
		t.Errorf("Status = %s, want %s", loaded.Status(), request.Status())
	}
	if loaded.Requester() != request.Requester() {
		t.Errorf("Requester = %q, want %q", loaded.Requester(), request.Requester())
	}
	if !loaded.DueAt().Equal(request.DueAt()) {
		t.Errorf("DueAt = %s, want %s", loaded.DueAt(), request.DueAt())
	}
	if loaded.Version() != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version())
	}
	if got := len(loaded.PendingEvents()); got != 0 {
		t.Errorf("a reloaded request holds %d uncommitted event(s), want 0", got)
	}
}

// replayEquivalence is the property that makes the stream the record rather than
// a log kept beside one. A request driven through its whole lifecycle and the
// same request rebuilt from the store must be indistinguishable, field for
// field; if they ever are not, every read model built from the stream is
// describing a request that never existed.
func replayEquivalence(t *testing.T, store Store) {
	ctx := context.Background()
	live := lifecycle(t, store, "PRR-2026-0041")

	rehydrated, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	comparisons := []struct {
		field string
		live  any
		rerun any
	}{
		{"ID", live.ID(), rehydrated.ID()},
		{"Requester", live.Requester(), rehydrated.Requester()},
		{"Description", live.Description(), rehydrated.Description()},
		{"Status", live.Status(), rehydrated.Status()},
		{"Reviewer", live.Reviewer(), rehydrated.Reviewer()},
		{"ReleasedPages", live.ReleasedPages(), rehydrated.ReleasedPages()},
		{"Exemption", live.Exemption(), rehydrated.Exemption()},
		{"PackageID", live.PackageID(), rehydrated.PackageID()},
		{"FailureCause", live.FailureCause(), rehydrated.FailureCause()},
		{"Version", live.Version(), rehydrated.Version()},
	}
	for _, comparison := range comparisons {
		if comparison.live != comparison.rerun {
			t.Errorf("%s: live = %v, rehydrated = %v",
				comparison.field, comparison.live, comparison.rerun)
		}
	}
	if !live.DueAt().Equal(rehydrated.DueAt()) {
		t.Errorf("DueAt: live = %s, rehydrated = %s", live.DueAt(), rehydrated.DueAt())
	}
	if !live.SubmittedAt().Equal(rehydrated.SubmittedAt()) {
		t.Errorf("SubmittedAt: live = %s, rehydrated = %s",
			live.SubmittedAt(), rehydrated.SubmittedAt())
	}
}

// metadataIsRecorded checks what is stored beside each fact. The traceparent is
// what lets a message published later belong to the trace of the request that
// caused it; the idempotency key is what lets an auditor see which command
// produced which facts.
func metadataIsRecorded(t *testing.T, store Store) {
	ctx := context.Background()
	_, write := submission(t, "PRR-2026-0041", "key-submit", day0)
	if _, err := store.Append(ctx, write); err != nil {
		t.Fatalf("Append: %v", err)
	}

	stream, err := store.Stream(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(stream) != 1 {
		t.Fatalf("stream length = %d, want 1", len(stream))
	}

	stored := stream[0]
	if stored.Metadata.TraceParent != traceParent {
		t.Errorf("traceparent = %q, want %q", stored.Metadata.TraceParent, traceParent)
	}
	if stored.Metadata.IdempotencyKey != "key-submit" {
		t.Errorf("idempotency key = %q, want %q", stored.Metadata.IdempotencyKey, "key-submit")
	}
	if stored.Version != 1 {
		t.Errorf("version = %d, want 1", stored.Version)
	}
	if stored.Sequence <= 0 {
		t.Errorf("sequence = %d, want a positive store-wide position", stored.Sequence)
	}
	if !stored.RecordedAt.Equal(day0) {
		t.Errorf("recorded at %s, want %s", stored.RecordedAt, day0)
	}
	if _, err := stored.Event(); err != nil {
		t.Errorf("the stored payload does not decode: %v", err)
	}
}

func appendNeedsKey(t *testing.T, store Store) {
	_, write := submission(t, "PRR-2026-0041", "", day0)

	_, err := store.Append(context.Background(), write)

	if !errors.Is(err, recordsrequest.ErrNoIdempotencyKey) {
		t.Fatalf("Append error = %v, want %v", err, recordsrequest.ErrNoIdempotencyKey)
	}
}

func appendNeedsEvents(t *testing.T, store Store) {
	write := recordsrequest.Append{
		RequestID:      "PRR-2026-0041",
		IdempotencyKey: "key-empty",
	}

	_, err := store.Append(context.Background(), write)

	if !errors.Is(err, recordsrequest.ErrNoChanges) {
		t.Fatalf("Append error = %v, want %v", err, recordsrequest.ErrNoChanges)
	}
}

// staleAppendIsRejected is the lost-update case: two officers read the same
// request, both act on it, and the second write must lose rather than quietly
// overwrite the first.
func staleAppendIsRejected(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)

	// Both officers read the request at the same version and both decided to
	// send the receipt notice. Neither knows about the other.
	first := acknowledgement(t, store, "PRR-2026-0041", "key-first")
	second := acknowledgement(t, store, "PRR-2026-0041", "key-second")

	if _, err := store.Append(ctx, first); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	_, err := store.Append(ctx, second)

	if !errors.Is(err, recordsrequest.ErrVersionConflict) {
		t.Fatalf("stale Append error = %v, want %v", err, recordsrequest.ErrVersionConflict)
	}
	loaded, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version() != 2 {
		t.Errorf("Version after a rejected append = %d, want 2", loaded.Version())
	}
}

// rejectedAppendQueuesNothing is the transactional half of the outbox: an append
// that was refused must leave no message behind. A message for a fact that was
// never recorded is worse than a lost one, because the consumer acts on it.
func rejectedAppendQueuesNothing(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	drain(t, store)

	stale := acknowledgement(t, store, "PRR-2026-0041", "key-stale")
	stale.ExpectedVersion = 0 // decided against a version the store has moved past

	if _, err := store.Append(ctx, stale); !errors.Is(err, recordsrequest.ErrVersionConflict) {
		t.Fatalf("Append error = %v, want %v", err, recordsrequest.ErrVersionConflict)
	}

	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a rejected append queued %d message(s), want 0", len(pending))
	}
}

func eventsAndEntriesCommitTogether(t *testing.T, store Store) {
	ctx := context.Background()
	_, write := submission(t, "PRR-2026-0041", "key-submit", day0)
	if _, err := store.Append(ctx, write); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != len(write.Events) {
		t.Fatalf("pending messages = %d, want %d", len(pending), len(write.Events))
	}

	// The message is built from the event, not from a copy stored beside it,
	// which is what makes a republished message identical to the first one.
	envelope := outbox.Message(pending[0])
	if err := envelope.Validate(); err != nil {
		t.Fatalf("the message built from the record is not a valid CloudEvent: %v", err)
	}
	if envelope.Type != "records_request.submitted" {
		t.Errorf("envelope type = %q, want %q", envelope.Type, "records_request.submitted")
	}
	if envelope.ID != outbox.MessageID("PRR-2026-0041", 1) {
		t.Errorf("envelope id = %q, want %q", envelope.ID, outbox.MessageID("PRR-2026-0041", 1))
	}
	if envelope.TraceParent != traceParent {
		t.Errorf("envelope traceparent = %q, want the recorded %q",
			envelope.TraceParent, traceParent)
	}
}

func unusedKeyIsNotRecorded(t *testing.T, store Store) {
	_, found, err := store.Recorded(context.Background(), "key-never-used")

	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if found {
		t.Error("an unused idempotency key was reported as recorded")
	}
}

// replayedKeyAppendsNothing is the double-submit case in its simplest form: the
// operator's browser retried, the same key arrived twice, and the request must
// carry one event and one message.
func replayedKeyAppendsNothing(t *testing.T, store Store) {
	ctx := context.Background()
	_, write := submission(t, "PRR-2026-0041", "key-submit", day0)

	first, err := store.Append(ctx, write)
	if err != nil {
		t.Fatalf("first Append: %v", err)
	}
	second, err := store.Append(ctx, write)
	if err != nil {
		t.Fatalf("replayed Append: %v", err)
	}

	if !second.Replayed {
		t.Error("the replayed result is not marked as a replay")
	}
	if second.Version != first.Version || second.RequestID != first.RequestID {
		t.Errorf("replayed result = %+v, want the original %+v", second, first)
	}
	loaded, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version() != 1 {
		t.Errorf("Version after a replayed append = %d, want 1", loaded.Version())
	}
	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending messages after a replayed append = %d, want 1", len(pending))
	}

	recorded, found, err := store.Recorded(ctx, "key-submit")
	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if !found || recorded.Version != first.Version {
		t.Errorf("Recorded = (%+v, %t), want the original result", recorded, found)
	}
}

// streamReadsInCommitOrder covers the read a projection depends on: events from
// every stream, in the order they were committed, after a checkpoint.
func streamReadsInCommitOrder(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-a", day0)
	commitSubmission(t, store, "PRR-2026-0100", "key-b", day0.Add(time.Hour))
	ack := acknowledgement(t, store, "PRR-2026-0041", "key-c")
	if _, err := store.Append(ctx, ack); err != nil {
		t.Fatalf("Append: %v", err)
	}

	all, err := store.Stream(ctx, 0, 100)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("stream length = %d, want 3", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Sequence <= all[i-1].Sequence {
			t.Fatalf("sequences are not increasing: %d then %d",
				all[i-1].Sequence, all[i].Sequence)
		}
	}
	if all[2].RequestID != "PRR-2026-0041" || all[2].Version != 2 {
		t.Errorf("last event = %s v%d, want PRR-2026-0041 v2",
			all[2].RequestID, all[2].Version)
	}

	rest, err := store.Stream(ctx, all[0].Sequence, 100)
	if err != nil {
		t.Fatalf("Stream after a checkpoint: %v", err)
	}
	if len(rest) != 2 {
		t.Fatalf("stream after the first event = %d, want 2", len(rest))
	}

	page, err := store.Stream(ctx, 0, 2)
	if err != nil {
		t.Fatalf("Stream with a limit: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("stream page = %d, want 2", len(page))
	}
}

func pendingIsOrdered(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	ack := acknowledgement(t, store, "PRR-2026-0041", "key-ack")
	if _, err := store.Append(ctx, ack); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending messages = %d, want 2", len(pending))
	}
	for i, want := range []int{1, 2} {
		if pending[i].Version != want {
			t.Fatalf("pending[%d] is version %d, want %d", i, pending[i].Version, want)
		}
		if i > 0 && pending[i].Sequence <= pending[i-1].Sequence {
			t.Fatalf("sequences are not increasing: %d then %d",
				pending[i-1].Sequence, pending[i].Sequence)
		}
	}
}

func dispatchedStopsBeingPending(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	ack := acknowledgement(t, store, "PRR-2026-0041", "key-ack")
	if _, err := store.Append(ctx, ack); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if err := store.MarkDispatched(ctx, pending[0].Sequence); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}

	remaining, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("pending messages = %d, want 1", len(remaining))
	}
	if remaining[0].Sequence == pending[0].Sequence {
		t.Error("a dispatched message is still pending")
	}
}

func markingTwiceIsNotAnError(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}

	// A relay that published, died before marking, and published again on
	// restart marks the same entry twice. That is the ordinary path, not an
	// error path.
	if err := store.MarkDispatched(ctx, pending[0].Sequence); err != nil {
		t.Fatalf("first MarkDispatched: %v", err)
	}
	if err := store.MarkDispatched(ctx, pending[0].Sequence); err != nil {
		t.Fatalf("second MarkDispatched: %v", err)
	}
}

func projectionCatchesUp(t *testing.T, store Store) {
	ctx := context.Background()
	// Identifiers that sort the other way round from their submission times, so
	// a projection that returns them in key order fails here.
	commitSubmission(t, store, "PRR-2026-0900", "key-a", day0)
	commitSubmission(t, store, "PRR-2026-0100", "key-b", day0.Add(time.Hour))
	ack := acknowledgement(t, store, "PRR-2026-0900", "key-c")
	if _, err := store.Append(ctx, ack); err != nil {
		t.Fatalf("Append: %v", err)
	}

	applied, err := projector.New(store, store, 0, nil).CatchUp(ctx)
	if err != nil {
		t.Fatalf("CatchUp: %v", err)
	}
	if applied != 3 {
		t.Errorf("applied %d event(s), want 3", applied)
	}

	summaries, err := store.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want 2", len(summaries))
	}
	if summaries[0].RequestID != "PRR-2026-0900" || summaries[1].RequestID != "PRR-2026-0100" {
		t.Fatalf("summaries are in the wrong order: %s then %s",
			summaries[0].RequestID, summaries[1].RequestID)
	}
	if summaries[0].Status != recordsrequest.StatusAcknowledged {
		t.Errorf("status = %s, want %s", summaries[0].Status, recordsrequest.StatusAcknowledged)
	}
	if summaries[0].Version != 2 {
		t.Errorf("version = %d, want 2", summaries[0].Version)
	}
	if !summaries[0].SubmittedAt.Equal(day0) {
		t.Errorf("submitted at %s, want %s", summaries[0].SubmittedAt, day0)
	}
}

// projectionIgnoresRepeats is what makes a projector safe to restart: a batch it
// already saved must be a no-op when it arrives again, rather than a second
// write or an error.
func projectionIgnoresRepeats(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	stream, err := store.Stream(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	request, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	summary := recordsrequest.SummaryOf(request)
	through := stream[len(stream)-1].Sequence

	if err := store.Save(ctx, []recordsrequest.Summary{summary}, through); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// The same batch again, with a summary that is deliberately wrong. A store
	// that applies it has no checkpoint check.
	stale := summary
	stale.Status = recordsrequest.StatusDenied
	if err := store.Save(ctx, []recordsrequest.Summary{stale}, through); err != nil {
		t.Fatalf("repeated Save: %v", err)
	}

	checkpoint, err := store.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if checkpoint != through {
		t.Errorf("checkpoint = %d, want %d", checkpoint, through)
	}
	summaries, err := store.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Status != recordsrequest.StatusOpen {
		t.Fatalf("summaries = %+v, want one open request", summaries)
	}
}

// projectionRebuilds proves the read model holds nothing that is not derivable
// from the stream: catching up twice from the same events produces the same
// rows, which is what makes deleting and rebuilding it a safe repair.
func projectionRebuilds(t *testing.T, store Store) {
	ctx := context.Background()
	lifecycle(t, store, "PRR-2026-0041")

	catchUp := projector.New(store, store, 0, nil)
	if _, err := catchUp.CatchUp(ctx); err != nil {
		t.Fatalf("CatchUp: %v", err)
	}
	first, err := store.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}

	// A second pass has nothing to do, and must leave the model as it was.
	applied, err := catchUp.CatchUp(ctx)
	if err != nil {
		t.Fatalf("second CatchUp: %v", err)
	}
	if applied != 0 {
		t.Errorf("a caught-up projection applied %d event(s), want 0", applied)
	}
	second, err := store.Summaries(ctx)
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(first) != len(second) || first[0] != second[0] {
		t.Errorf("summaries changed on a second pass: %+v then %+v", first, second)
	}
	if first[0].Status != recordsrequest.StatusFulfilled {
		t.Errorf("status = %s, want %s", first[0].Status, recordsrequest.StatusFulfilled)
	}
}

// interleavedWritersProduceOneEvent is the concurrency case for optimistic
// locking. Every writer read the same version and every writer is about to
// write. Exactly one may win, the rest must be told the request changed, and the
// stream must be one event longer -- not two, and not zero.
func interleavedWritersProduceOneEvent(t *testing.T, store Store) {
	ctx := context.Background()
	commitSubmission(t, store, "PRR-2026-0041", "key-submit", day0)
	ack := acknowledgement(t, store, "PRR-2026-0041", "key-ack")
	if _, err := store.Append(ctx, ack); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Each racer stages its own assignment against version 2, with a distinct
	// reviewer and a distinct idempotency key: these are genuinely different
	// writes, so nothing but the version check can decide between them.
	writes := make([]recordsrequest.Append, racers)
	for i := range writes {
		writes[i] = assignment(t, store, "PRR-2026-0041",
			fmt.Sprintf("records.officer.%d", i), fmt.Sprintf("key-assign-%d", i))
	}

	results, errs := race(func(i int) (recordsrequest.Result, error) {
		return store.Append(ctx, writes[i])
	})

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
			if results[i].Version != 3 {
				t.Errorf("winning result version = %d, want 3", results[i].Version)
			}
		case errors.Is(err, recordsrequest.ErrVersionConflict):
		default:
			t.Errorf("writer %d: unexpected error %v", i, err)
		}
	}
	if won != 1 {
		t.Errorf("%d of %d writers won, want exactly 1", won, racers)
	}

	loaded, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version() != 3 {
		t.Errorf("Version after %d interleaved writers = %d, want 3", racers, loaded.Version())
	}
	pending, err := store.PendingOutbox(ctx, 100)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 3 {
		t.Errorf("pending messages = %d, want 3 (one per committed event)", len(pending))
	}
}

// doubleSubmissionProducesOneEvent is the idempotency case under concurrency:
// the operator's browser retried the same command several times at once. Every
// caller must be told the same thing, and the request must carry one event.
func doubleSubmissionProducesOneEvent(t *testing.T, store Store) {
	ctx := context.Background()
	_, write := submission(t, "PRR-2026-0041", "key-submit", day0)

	results, errs := race(func(int) (recordsrequest.Result, error) {
		return store.Append(ctx, write)
	})

	applied := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("submission %d: %v", i, err)
		}
		if !results[i].Replayed {
			applied++
		}
		if results[i].Version != 1 {
			t.Errorf("submission %d returned version %d, want 1", i, results[i].Version)
		}
	}
	if applied != 1 {
		t.Errorf("%d of %d submissions were applied, want exactly 1", applied, racers)
	}

	loaded, err := store.Load(ctx, "PRR-2026-0041")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version() != 1 {
		t.Errorf("Version after %d identical submissions = %d, want 1", racers, loaded.Version())
	}
	pending, err := store.PendingOutbox(ctx, 100)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending messages = %d, want 1", len(pending))
	}
}

// race runs attempt on every racer at once and collects what each one got. The
// goroutines are released together so the writes genuinely interleave rather
// than lining up behind whichever goroutine the scheduler started first.
func race(attempt func(i int) (recordsrequest.Result, error)) ([]recordsrequest.Result, []error) {
	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		results = make([]recordsrequest.Result, racers)
		errs    = make([]error, racers)
	)
	start.Add(1)
	for i := range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[i], errs[i] = attempt(i)
		}()
	}
	start.Done()
	done.Wait()
	return results, errs
}

// lifecycle drives one request through submission, acknowledgment, assignment,
// a release that fails, a release that succeeds, and delivery, storing every
// step. It returns the live aggregate so a case can compare it with the one the
// store rebuilds.
func lifecycle(t *testing.T, store Store, requestID string) *recordsrequest.Request {
	t.Helper()
	ctx := context.Background()

	live, write := submission(t, requestID, "key-submit", day0)
	if _, err := store.Append(ctx, write); err != nil {
		t.Fatalf("Append submission: %v", err)
	}
	live.MarkCommitted()

	steps := []struct {
		name    string
		key     string
		command func() error
	}{
		{"acknowledge", "key-ack", func() error {
			return live.Acknowledge(recordsrequest.Acknowledge{At: day0.Add(time.Hour)})
		}},
		{"assign", "key-assign", func() error {
			return live.AssignReviewer(recordsrequest.AssignReviewer{
				Reviewer: "records.officer.7", At: day0.Add(2 * time.Hour)})
		}},
		{"release", "key-release", func() error {
			return live.Fulfill(recordsrequest.Fulfill{ReleasedPages: 18, At: day0.Add(3 * time.Hour)})
		}},
		{"compensate", "key-compensate", func() error {
			return live.FailDelivery(recordsrequest.FailDelivery{
				Reason: "two pages are still under legal hold", At: day0.Add(4 * time.Hour)})
		}},
		{"release again", "key-rerelease", func() error {
			return live.Fulfill(recordsrequest.Fulfill{ReleasedPages: 16, At: day0.Add(5 * time.Hour)})
		}},
		{"confirm delivery", "key-delivered", func() error {
			return live.ConfirmDelivery(recordsrequest.ConfirmDelivery{
				PackageID: "PKG-2026-0188", At: day0.Add(6 * time.Hour)})
		}},
	}
	for _, step := range steps {
		if err := step.command(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if _, err := store.Append(ctx, appendOf(live, step.key)); err != nil {
			t.Fatalf("Append %s: %v", step.name, err)
		}
		live.MarkCommitted()
	}
	return live
}

// submission stages the first append for a new request: the aggregate the
// command produced, and the append that stores it.
func submission(t *testing.T, requestID, key string, at time.Time) (*recordsrequest.Request,
	recordsrequest.Append) {
	t.Helper()
	request := recordsrequest.New()
	if err := request.Submit(recordsrequest.Submit{
		RequestID:   requestID,
		Requester:   "M. Alvarez",
		Description: "Inspection reports for the Fifth Street bridge, 2025",
		At:          at,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return request, appendOf(request, key)
}

// commitSubmission stores a new request and fails the test if the store refuses.
func commitSubmission(t *testing.T, store Store, requestID, key string, at time.Time) {
	t.Helper()
	_, write := submission(t, requestID, key, at)
	if _, err := store.Append(context.Background(), write); err != nil {
		t.Fatalf("Append %s: %v", requestID, err)
	}
}

// acknowledgement stages a receipt notice against the stored request.
func acknowledgement(t *testing.T, store Store, requestID, key string) recordsrequest.Append {
	t.Helper()
	request := load(t, store, requestID)
	if err := request.Acknowledge(recordsrequest.Acknowledge{At: day0.Add(time.Hour)}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	return appendOf(request, key)
}

// assignment stages a reviewer assignment against the stored request.
func assignment(t *testing.T, store Store, requestID, reviewer, key string) recordsrequest.Append {
	t.Helper()
	request := load(t, store, requestID)
	if err := request.AssignReviewer(recordsrequest.AssignReviewer{
		Reviewer: reviewer,
		At:       day0.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("AssignReviewer: %v", err)
	}
	return appendOf(request, key)
}

// load reads a request back out of the store.
func load(t *testing.T, store Store, requestID string) *recordsrequest.Request {
	t.Helper()
	request, err := store.Load(context.Background(), requestID)
	if err != nil {
		t.Fatalf("Load %s: %v", requestID, err)
	}
	return request
}

// appendOf turns the events a command raised into the append that stores them.
func appendOf(request *recordsrequest.Request, key string) recordsrequest.Append {
	return recordsrequest.Append{
		RequestID:       request.ID(),
		ExpectedVersion: request.CommittedVersion(),
		IdempotencyKey:  key,
		TraceParent:     traceParent,
		Events:          request.PendingEvents(),
	}
}

// drain marks every pending message dispatched, for cases that care about what a
// later append queues rather than about the whole history.
func drain(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	pending, err := store.PendingOutbox(ctx, 100)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	for _, stored := range pending {
		if err := store.MarkDispatched(ctx, stored.Sequence); err != nil {
			t.Fatalf("MarkDispatched: %v", err)
		}
	}
}
