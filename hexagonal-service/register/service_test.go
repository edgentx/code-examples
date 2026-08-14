package register_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/memorystore"
	"github.com/edgentx/code-examples/hexagonal-service/permit"
	"github.com/edgentx/code-examples/hexagonal-service/register"
)

// Every test in this file is a full use case, and none of them starts a
// database, opens a file or waits on a network. That is the payoff of the port:
// the twin is a real permit.Repository that passes the same contract as SQLite,
// so the application service cannot tell the difference and neither can these
// assertions.

var day0 = time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC)

// fixedClock freezes time so "expiring within 30 days" is a stable assertion.
func fixedClock(at time.Time) register.Clock {
	return func() time.Time { return at }
}

// newService wires the service to a fresh in-memory register.
func newService(t *testing.T, at time.Time) *register.Service {
	t.Helper()
	return register.New(memorystore.New(), fixedClock(at))
}

func TestIssue(t *testing.T) {
	tests := []struct {
		name    string
		command register.IssueCommand
		wantErr error
	}{
		{
			name: "a complete application goes on the register",
			command: register.IssueCommand{
				Number: "BP-2026-00417", Holder: "Ridgeline Builders",
				Kind: permit.KindBuilding, Site: "1400 Canal Street",
			},
		},
		{
			name: "an application without a holder is refused",
			command: register.IssueCommand{
				Number: "BP-2026-00418", Kind: permit.KindBuilding,
				Site: "1400 Canal Street",
			},
			wantErr: permit.ErrMissingField,
		},
		{
			name: "an application for a kind the agency does not issue is refused",
			command: register.IssueCommand{
				Number: "XX-2026-00001", Holder: "Ridgeline Builders",
				Kind: permit.Kind("demolition"), Site: "1400 Canal Street",
			},
			wantErr: permit.ErrUnknownKind,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newService(t, day0)

			issued, err := service.Issue(context.Background(), test.command)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Issue error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				return
			}
			if issued.Version != 1 {
				t.Errorf("Version = %d, want 1", issued.Version)
			}
			if want := day0.AddDate(1, 0, 0); !issued.ExpiresOn.Equal(want) {
				t.Errorf("ExpiresOn = %s, want %s", issued.ExpiresOn, want)
			}
		})
	}
}

func TestIssueRefusesAReusedNumber(t *testing.T) {
	ctx := context.Background()
	service := newService(t, day0)
	command := register.IssueCommand{
		Number: "BP-2026-00417", Holder: "Ridgeline Builders",
		Kind: permit.KindBuilding, Site: "1400 Canal Street",
	}
	if _, err := service.Issue(ctx, command); err != nil {
		t.Fatalf("first Issue: %v", err)
	}

	command.Holder = "Harbor Mechanical"
	_, err := service.Issue(ctx, command)

	if !errors.Is(err, permit.ErrDuplicateNumber) {
		t.Fatalf("Issue error = %v, want %v", err, permit.ErrDuplicateNumber)
	}
}

func TestSuspend(t *testing.T) {
	ctx := context.Background()
	service := newService(t, day0)
	issued, err := service.Issue(ctx, register.IssueCommand{
		Number: "EP-2026-00218", Holder: "Harbor Mechanical",
		Kind: permit.KindElectrical, Site: "22 Quarry Road",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	suspended, err := service.Suspend(ctx, issued.Number)
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	if suspended.Status != permit.StatusSuspended {
		t.Errorf("Status = %s, want %s", suspended.Status, permit.StatusSuspended)
	}
	if suspended.Version != issued.Version+1 {
		t.Errorf("Version = %d, want %d", suspended.Version, issued.Version+1)
	}
	// The rule the use case is defending: a permit already stopped cannot be
	// stopped again, and the second attempt never reaches the store.
	if _, err := service.Suspend(ctx, issued.Number); !errors.Is(err, permit.ErrNotActive) {
		t.Errorf("second Suspend error = %v, want %v", err, permit.ErrNotActive)
	}
}

func TestSuspendAnUnregisteredPermit(t *testing.T) {
	service := newService(t, day0)

	_, err := service.Suspend(context.Background(), "BP-2026-99999")

	if !errors.Is(err, permit.ErrNotFound) {
		t.Errorf("Suspend error = %v, want %v", err, permit.ErrNotFound)
	}
}

func TestRenewCannotRescueASuspendedPermit(t *testing.T) {
	ctx := context.Background()
	service := newService(t, day0)
	issued, err := service.Issue(ctx, register.IssueCommand{
		Number: "PP-2026-00590", Holder: "Harbor Mechanical",
		Kind: permit.KindPlumbing, Site: "9 Foundry Lane",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := service.Suspend(ctx, issued.Number); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	_, err = service.Renew(ctx, issued.Number)

	if !errors.Is(err, permit.ErrNotActive) {
		t.Errorf("Renew error = %v, want %v", err, permit.ErrNotActive)
	}
}

func TestRenewalNotices(t *testing.T) {
	ctx := context.Background()
	// Two services over one register: the counter clerk issuing permits on day
	// zero, and the notice run reading the same store eleven months later. Only
	// the clock differs, which is the whole reason it is a port.
	store := memorystore.New()
	service := register.New(store, fixedClock(day0))
	noticeRun := register.New(store, fixedClock(day0.AddDate(0, 11, 5)))

	for _, command := range []register.IssueCommand{
		{Number: "PP-2026-00590", Holder: "Harbor Mechanical",
			Kind: permit.KindPlumbing, Site: "9 Foundry Lane"},
		{Number: "BP-2026-00417", Holder: "Ridgeline Builders",
			Kind: permit.KindBuilding, Site: "1400 Canal Street"},
		{Number: "EP-2026-00218", Holder: "Harbor Mechanical",
			Kind: permit.KindElectrical, Site: "22 Quarry Road"},
	} {
		if _, err := service.Issue(ctx, command); err != nil {
			t.Fatalf("Issue(%s): %v", command.Number, err)
		}
	}
	// Suspended permits authorize nothing, so they are not renewal candidates.
	if _, err := service.Suspend(ctx, "PP-2026-00590"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	due, err := noticeRun.RenewalNotices(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("RenewalNotices: %v", err)
	}

	want := []string{"BP-2026-00417", "EP-2026-00218"}
	if len(due) != len(want) {
		t.Fatalf("got %d notice(s), want %d: %+v", len(due), len(want), due)
	}
	for i, number := range want {
		if due[i].Number != number {
			t.Errorf("notice %d = %s, want %s", i, due[i].Number, number)
		}
	}
}

func TestRenewalNoticesRequiresAWindow(t *testing.T) {
	_, err := newService(t, day0).RenewalNotices(context.Background(), 0)

	if !errors.Is(err, permit.ErrMissingField) {
		t.Errorf("RenewalNotices error = %v, want %v", err, permit.ErrMissingField)
	}
}
