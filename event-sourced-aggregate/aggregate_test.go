package recordsrequest_test

import (
	"errors"
	"testing"
	"time"

	recordsrequest "github.com/edgentx/code-examples/event-sourced-aggregate"
)

var (
	day0 = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)
	day1 = day0.Add(24 * time.Hour)
	day2 = day0.Add(48 * time.Hour)
)

// submitted is the history of a request that exists but has not been
// acknowledged.
func submitted() []recordsrequest.Event {
	return []recordsrequest.Event{
		recordsrequest.Submitted{
			RequestID:   "PRR-2026-0041",
			Requester:   "M. Alvarez",
			Description: "Inspection reports for the Fifth Street bridge, 2025",
			At:          day0,
			DueAt:       day0.Add(10 * 24 * time.Hour),
		},
	}
}

// acknowledged extends submitted with the statutory receipt notice.
func acknowledged() []recordsrequest.Event {
	return append(submitted(), recordsrequest.Acknowledged{RequestID: "PRR-2026-0041", At: day1})
}

// assigned extends acknowledged with a named records officer.
func assigned() []recordsrequest.Event {
	return append(acknowledged(), recordsrequest.ReviewerAssigned{
		RequestID: "PRR-2026-0041",
		Reviewer:  "records.officer.7",
		At:        day1,
	})
}

// closed extends assigned with a fulfillment.
func closed() []recordsrequest.Event {
	return append(assigned(), recordsrequest.Fulfilled{
		RequestID:     "PRR-2026-0041",
		ReleasedPages: 18,
		At:            day2,
	})
}

func TestCommandInvariants(t *testing.T) {
	tests := []struct {
		name    string
		history []recordsrequest.Event
		command func(*recordsrequest.Request) error
		wantErr error
	}{
		{
			name:    "submit opens a new request",
			history: nil,
			command: func(r *recordsrequest.Request) error {
				return r.Submit(recordsrequest.Submit{
					RequestID:   "PRR-2026-0041",
					Requester:   "M. Alvarez",
					Description: "Inspection reports",
					At:          day0,
				})
			},
		},
		{
			name:    "submit rejects a reused identifier",
			history: submitted(),
			command: func(r *recordsrequest.Request) error {
				return r.Submit(recordsrequest.Submit{
					RequestID:   "PRR-2026-0041",
					Requester:   "M. Alvarez",
					Description: "Inspection reports",
					At:          day1,
				})
			},
			wantErr: recordsrequest.ErrAlreadySubmitted,
		},
		{
			name:    "submit requires a description",
			history: nil,
			command: func(r *recordsrequest.Request) error {
				return r.Submit(recordsrequest.Submit{
					RequestID: "PRR-2026-0041",
					Requester: "M. Alvarez",
					At:        day0,
				})
			},
			wantErr: recordsrequest.ErrMissingField,
		},
		{
			name:    "acknowledge requires an existing request",
			history: nil,
			command: func(r *recordsrequest.Request) error {
				return r.Acknowledge(recordsrequest.Acknowledge{At: day1})
			},
			wantErr: recordsrequest.ErrNotSubmitted,
		},
		{
			name:    "acknowledge is not repeatable",
			history: acknowledged(),
			command: func(r *recordsrequest.Request) error {
				return r.Acknowledge(recordsrequest.Acknowledge{At: day2})
			},
			wantErr: recordsrequest.ErrAlreadyAcknowledged,
		},
		{
			name:    "reviewer cannot be assigned before acknowledgment",
			history: submitted(),
			command: func(r *recordsrequest.Request) error {
				return r.AssignReviewer(recordsrequest.AssignReviewer{
					Reviewer: "records.officer.7",
					At:       day1,
				})
			},
			wantErr: recordsrequest.ErrNotAcknowledged,
		},
		{
			name:    "reassigning the current reviewer is rejected",
			history: assigned(),
			command: func(r *recordsrequest.Request) error {
				return r.AssignReviewer(recordsrequest.AssignReviewer{
					Reviewer: "records.officer.7",
					At:       day2,
				})
			},
			wantErr: recordsrequest.ErrSameReviewer,
		},
		{
			name:    "reassignment to a different officer is allowed",
			history: assigned(),
			command: func(r *recordsrequest.Request) error {
				return r.AssignReviewer(recordsrequest.AssignReviewer{
					Reviewer: "records.officer.9",
					At:       day2,
				})
			},
		},
		{
			name:    "fulfillment requires an accountable reviewer",
			history: acknowledged(),
			command: func(r *recordsrequest.Request) error {
				return r.Fulfill(recordsrequest.Fulfill{ReleasedPages: 4, At: day2})
			},
			wantErr: recordsrequest.ErrNoReviewer,
		},
		{
			name:    "fulfillment rejects an impossible page count",
			history: assigned(),
			command: func(r *recordsrequest.Request) error {
				return r.Fulfill(recordsrequest.Fulfill{ReleasedPages: -1, At: day2})
			},
			wantErr: recordsrequest.ErrNegativePages,
		},
		{
			name:    "denial requires an exemption citation",
			history: assigned(),
			command: func(r *recordsrequest.Request) error {
				return r.Deny(recordsrequest.Deny{Exemption: "   ", At: day2})
			},
			wantErr: recordsrequest.ErrMissingField,
		},
		{
			name:    "denial with a citation closes the request",
			history: assigned(),
			command: func(r *recordsrequest.Request) error {
				return r.Deny(recordsrequest.Deny{
					Exemption: "personnel file exemption",
					At:        day2,
				})
			},
		},
		{
			name:    "a closed request accepts no further commands",
			history: closed(),
			command: func(r *recordsrequest.Request) error {
				return r.Deny(recordsrequest.Deny{Exemption: "any", At: day2})
			},
			wantErr: recordsrequest.ErrClosed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := recordsrequest.FromHistory(test.history)
			if err != nil {
				t.Fatalf("loading history: %v", err)
			}
			versionBefore := request.Version()

			err = test.command(request)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("command error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				if got := len(request.PendingEvents()); got != 0 {
					t.Errorf("rejected command raised %d event(s), want 0", got)
				}
				if request.Version() != versionBefore {
					t.Errorf("rejected command moved version to %d, want %d",
						request.Version(), versionBefore)
				}
				return
			}
			if got := len(request.PendingEvents()); got != 1 {
				t.Errorf("accepted command raised %d event(s), want 1", got)
			}
			if request.Version() != versionBefore+1 {
				t.Errorf("version = %d, want %d", request.Version(), versionBefore+1)
			}
		})
	}
}

func TestSubmitFixesTheStatutoryDeadline(t *testing.T) {
	request := recordsrequest.New()

	if err := request.Submit(recordsrequest.Submit{
		RequestID:   "PRR-2026-0041",
		Requester:   "M. Alvarez",
		Description: "Inspection reports",
		At:          day0,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	want := day0.Add(10 * 24 * time.Hour)
	if !request.DueAt().Equal(want) {
		t.Errorf("DueAt = %s, want %s", request.DueAt(), want)
	}
	if request.Status() != recordsrequest.StatusOpen {
		t.Errorf("Status = %s, want %s", request.Status(), recordsrequest.StatusOpen)
	}
}

func TestMarkCommittedClearsPendingEvents(t *testing.T) {
	request, err := recordsrequest.FromHistory(submitted())
	if err != nil {
		t.Fatalf("loading history: %v", err)
	}
	if got := len(request.PendingEvents()); got != 0 {
		t.Fatalf("replay queued %d event(s) for storage, want 0", got)
	}

	if err := request.Acknowledge(recordsrequest.Acknowledge{At: day1}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	pending := request.PendingEvents()
	if len(pending) != 1 {
		t.Fatalf("PendingEvents length = %d, want 1", len(pending))
	}

	request.MarkCommitted()
	if got := len(request.PendingEvents()); got != 0 {
		t.Errorf("PendingEvents after MarkCommitted = %d, want 0", got)
	}
	if len(pending) != 1 {
		t.Errorf("caller's slice was mutated by MarkCommitted, length = %d", len(pending))
	}
}
