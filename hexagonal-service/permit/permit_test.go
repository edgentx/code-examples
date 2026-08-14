package permit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/edgentx/code-examples/hexagonal-service/permit"
)

var day0 = time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC)

// active is a permit as it stands after issue.
func active(t *testing.T) permit.Permit {
	t.Helper()
	p, err := permit.Issue("BP-2026-00417", "Ridgeline Builders", permit.KindBuilding,
		"1400 Canal Street", day0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return p
}

// suspended is a permit an inspector has stopped work under.
func suspended(t *testing.T) permit.Permit {
	t.Helper()
	p, err := active(t).Suspend()
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	return p
}

func TestIssueValidation(t *testing.T) {
	tests := []struct {
		name    string
		number  string
		holder  string
		kind    permit.Kind
		site    string
		issued  time.Time
		wantErr error
	}{
		{
			name:   "a complete application is issued",
			number: "BP-2026-00417", holder: "Ridgeline Builders",
			kind: permit.KindBuilding, site: "1400 Canal Street", issued: day0,
		},
		{
			name:   "a permit number is required",
			number: "  ", holder: "Ridgeline Builders",
			kind: permit.KindBuilding, site: "1400 Canal Street", issued: day0,
			wantErr: permit.ErrMissingField,
		},
		{
			name:   "an accountable holder is required",
			number: "BP-2026-00417", holder: "",
			kind: permit.KindBuilding, site: "1400 Canal Street", issued: day0,
			wantErr: permit.ErrMissingField,
		},
		{
			name:   "a site is required",
			number: "BP-2026-00417", holder: "Ridgeline Builders",
			kind: permit.KindBuilding, site: "", issued: day0,
			wantErr: permit.ErrMissingField,
		},
		{
			name:   "an issue date is required",
			number: "BP-2026-00417", holder: "Ridgeline Builders",
			kind: permit.KindBuilding, site: "1400 Canal Street",
			wantErr: permit.ErrMissingField,
		},
		{
			name:   "the register does not issue unknown permit kinds",
			number: "BP-2026-00417", holder: "Ridgeline Builders",
			kind: permit.Kind("demolition"), site: "1400 Canal Street", issued: day0,
			wantErr: permit.ErrUnknownKind,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issued, err := permit.Issue(test.number, test.holder, test.kind, test.site, test.issued)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Issue error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				return
			}
			if issued.Status != permit.StatusActive {
				t.Errorf("Status = %s, want %s", issued.Status, permit.StatusActive)
			}
			if want := test.issued.AddDate(1, 0, 0); !issued.ExpiresOn.Equal(want) {
				t.Errorf("ExpiresOn = %s, want %s", issued.ExpiresOn, want)
			}
			if issued.Version != 0 {
				t.Errorf("Version = %d, want 0: nothing has stored it yet", issued.Version)
			}
		})
	}
}

func TestTransitions(t *testing.T) {
	tests := []struct {
		name       string
		start      func(*testing.T) permit.Permit
		transition func(permit.Permit) (permit.Permit, error)
		wantStatus permit.Status
		wantErr    error
	}{
		{
			name:  "an active permit can be suspended",
			start: active,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Suspend()
			},
			wantStatus: permit.StatusSuspended,
		},
		{
			name:  "a suspended permit cannot be suspended again",
			start: suspended,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Suspend()
			},
			wantErr: permit.ErrNotActive,
		},
		{
			name:  "a suspended permit can be reinstated",
			start: suspended,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Reinstate()
			},
			wantStatus: permit.StatusActive,
		},
		{
			name:  "an active permit is not reinstatable",
			start: active,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Reinstate()
			},
			wantErr: permit.ErrNotSuspended,
		},
		{
			name:  "an active permit can be renewed",
			start: active,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Renew(day0.AddDate(1, 0, 0))
			},
			wantStatus: permit.StatusActive,
		},
		{
			name:  "a suspended permit cannot be renewed",
			start: suspended,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Renew(day0.AddDate(1, 0, 0))
			},
			wantErr: permit.ErrNotActive,
		},
		{
			name:  "renewal requires a date",
			start: active,
			transition: func(p permit.Permit) (permit.Permit, error) {
				return p.Renew(time.Time{})
			},
			wantErr: permit.ErrMissingField,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := test.start(t)

			got, err := test.transition(start)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("transition error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				// A rejected transition must leave the caller's permit alone;
				// value semantics make that structural rather than a promise.
				if got != (permit.Permit{}) {
					t.Errorf("rejected transition returned %+v, want the zero permit", got)
				}
				return
			}
			if got.Status != test.wantStatus {
				t.Errorf("Status = %s, want %s", got.Status, test.wantStatus)
			}
		})
	}
}

func TestRenewalMovesTheExpiryOutAFullTerm(t *testing.T) {
	renewedOn := day0.AddDate(0, 11, 0)

	renewed, err := active(t).Renew(renewedOn)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if want := renewedOn.AddDate(1, 0, 0); !renewed.ExpiresOn.Equal(want) {
		t.Errorf("ExpiresOn = %s, want %s", renewed.ExpiresOn, want)
	}
	if renewed.Expired(renewedOn) {
		t.Error("a permit renewed today reports as expired today")
	}
	if !renewed.Expired(renewed.ExpiresOn) {
		t.Error("a permit does not report as expired on its expiry date")
	}
}
