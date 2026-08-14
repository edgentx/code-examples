package recordsrequest_test

import (
	"errors"
	"testing"
	"time"

	recordsrequest "github.com/edgentx/code-examples/event-sourced-aggregate"
)

// unknownEvent stands in for an event written by a newer version of the service
// than the one doing the replay.
type unknownEvent struct{ at time.Time }

func (e unknownEvent) EventName() string     { return "records_request.transferred" }
func (e unknownEvent) OccurredAt() time.Time { return e.at }

// TestReplayReconstructsState is the test that makes event sourcing safe to
// operate: a request driven through its whole lifecycle by commands, and the
// same request rebuilt from nothing but its stored events, must be
// indistinguishable. If this passes, the event stream is the system of record
// and any read model can be rebuilt from it.
func TestReplayReconstructsState(t *testing.T) {
	live := recordsrequest.New()

	steps := []struct {
		name    string
		command func() error
	}{
		{"submit", func() error {
			return live.Submit(recordsrequest.Submit{
				RequestID:   "PRR-2026-0041",
				Requester:   "M. Alvarez",
				Description: "Inspection reports for the Fifth Street bridge, 2025",
				At:          day0,
			})
		}},
		{"acknowledge", func() error {
			return live.Acknowledge(recordsrequest.Acknowledge{At: day1})
		}},
		{"assign", func() error {
			return live.AssignReviewer(recordsrequest.AssignReviewer{
				Reviewer: "records.officer.7",
				At:       day1,
			})
		}},
		{"reassign", func() error {
			return live.AssignReviewer(recordsrequest.AssignReviewer{
				Reviewer: "records.officer.9",
				At:       day2,
			})
		}},
		{"fulfill", func() error {
			return live.Fulfill(recordsrequest.Fulfill{ReleasedPages: 18, At: day2})
		}},
	}
	for _, step := range steps {
		if err := step.command(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}

	history := live.PendingEvents()
	if len(history) != len(steps) {
		t.Fatalf("history length = %d, want %d", len(history), len(steps))
	}

	replayed, err := recordsrequest.FromHistory(history)
	if err != nil {
		t.Fatalf("FromHistory: %v", err)
	}

	comparisons := []struct {
		field string
		live  any
		rerun any
	}{
		{"ID", live.ID(), replayed.ID()},
		{"Status", live.Status(), replayed.Status()},
		{"Reviewer", live.Reviewer(), replayed.Reviewer()},
		{"ReleasedPages", live.ReleasedPages(), replayed.ReleasedPages()},
		{"Exemption", live.Exemption(), replayed.Exemption()},
		{"Version", live.Version(), replayed.Version()},
	}
	for _, comparison := range comparisons {
		if comparison.live != comparison.rerun {
			t.Errorf("%s: live = %v, replayed = %v",
				comparison.field, comparison.live, comparison.rerun)
		}
	}
	if !live.DueAt().Equal(replayed.DueAt()) {
		t.Errorf("DueAt: live = %s, replayed = %s", live.DueAt(), replayed.DueAt())
	}
	if got := len(replayed.PendingEvents()); got != 0 {
		t.Errorf("replay queued %d event(s) for storage, want 0", got)
	}
}

// TestReplayRejectsUnknownEvents proves replay fails loudly rather than silently
// dropping a fact it does not understand. Skipping the event would produce an
// aggregate that looks valid and is wrong.
func TestReplayRejectsUnknownEvents(t *testing.T) {
	history := append(acknowledged(), unknownEvent{at: day2})

	if _, err := recordsrequest.FromHistory(history); !errors.Is(err, recordsrequest.ErrUnknownEvent) {
		t.Fatalf("FromHistory error = %v, want %v", err, recordsrequest.ErrUnknownEvent)
	}
}
