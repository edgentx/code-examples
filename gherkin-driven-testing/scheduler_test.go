package inspection_test

import (
	"testing"

	inspection "github.com/edgentx/code-examples/gherkin-driven-testing"
)

// northgate is a scheduler with one district that can staff two ordinary
// appointments a day and hold one back for emergencies, and one observed
// holiday.
func northgate(t *testing.T) *inspection.Scheduler {
	t.Helper()
	scheduler := inspection.NewScheduler(holidayCalendar(t))
	scheduler.SetCapacity("Northgate", inspection.Capacity{Routine: 2, Reserve: 1})
	return scheduler
}

// request builds an active standard request for Northgate; each test overrides
// only the field it is about.
func request(t *testing.T, filed, wanted string) inspection.Request {
	t.Helper()
	return inspection.Request{
		PermitID: "BP-4417",
		Permit:   inspection.PermitActive,
		District: "Northgate",
		Priority: inspection.PriorityStandard,
		FiledOn:  mustDay(t, filed),
		WantedOn: mustDay(t, wanted),
	}
}

// TestRefusalPrecedence is the test the feature files cannot reasonably carry.
// Each case breaks several rules at once, and the assertion is which single
// reason the contractor is given. Precedence is a real decision -- it is the
// difference between a caller being told to fix their permit and being told to
// try a different day -- so it is pinned here case by case rather than being
// left to whatever order the guards happen to sit in.
func TestRefusalPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		filed  string
		wanted string
		// mutate breaks the additional rules this case is about; nil when the
		// dates alone are the whole story.
		mutate  func(inspection.Request) inspection.Request
		refusal inspection.Reason
	}{
		{
			name:   "an unserved district outranks every other fault",
			filed:  "2026-06-18",
			wanted: "2026-07-04",
			mutate: func(r inspection.Request) inspection.Request {
				r.District = "Riverbend"
				r.Permit = inspection.PermitExpired
				return r
			},
			refusal: inspection.ReasonUnknownDistrict,
		},
		{
			name:   "a suspended permit outranks the calendar and the notice period",
			filed:  "2026-06-18",
			wanted: "2026-07-04",
			mutate: func(r inspection.Request) inspection.Request {
				r.Permit = inspection.PermitSuspended
				return r
			},
			refusal: inspection.ReasonPermitNotActive,
		},
		{
			name:   "an unpublished priority is refused rather than given the best service",
			filed:  "2026-06-15",
			wanted: "2026-06-15",
			mutate: func(r inspection.Request) inspection.Request {
				r.Priority = inspection.Priority("life-safety")
				return r
			},
			refusal: inspection.ReasonUnknownPriority,
		},
		{
			name:   "a day already past outranks the calendar",
			filed:  "2026-07-06",
			wanted: "2026-07-04",
			mutate: func(r inspection.Request) inspection.Request {
				r.Priority = inspection.PriorityEmergency
				return r
			},
			refusal: inspection.ReasonDateInPast,
		},
		{
			name:    "a closed office outranks the notice period",
			filed:   "2026-07-02",
			wanted:  "2026-07-03",
			refusal: inspection.ReasonNotABusinessDay,
		},
		{
			name:   "an emergency does not buy a day the office is closed",
			filed:  "2026-07-03",
			wanted: "2026-07-03",
			mutate: func(r inspection.Request) inspection.Request {
				r.Priority = inspection.PriorityEmergency
				return r
			},
			refusal: inspection.ReasonNotABusinessDay,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			scheduler := northgate(t)
			candidate := request(t, testCase.filed, testCase.wanted)
			if testCase.mutate != nil {
				candidate = testCase.mutate(candidate)
			}
			decision := scheduler.Book(candidate)
			if decision.Booked {
				t.Fatalf("expected a refusal, got a booking in a %s slot", decision.Slot)
			}
			if decision.Reason != testCase.refusal {
				t.Fatalf("refusal = %q, want %q", decision.Reason, testCase.refusal)
			}
		})
	}
}

// TestRefusedRequestsConsumeNoCapacity is the invariant that keeps a day from
// being quietly eaten by traffic that was never scheduled. A refusal counted as
// a booking would close a district's day to the contractors who did comply.
func TestRefusedRequestsConsumeNoCapacity(t *testing.T) {
	scheduler := northgate(t)
	day := mustDay(t, "2026-06-18")

	shortNotice := request(t, "2026-06-17", "2026-06-18")
	if decision := scheduler.Book(shortNotice); decision.Booked {
		t.Fatalf("expected the short-notice request to be refused")
	}
	if got := scheduler.Booked("Northgate", day); got != 0 {
		t.Fatalf("refused request consumed capacity: %d appointments booked, want 0", got)
	}

	for i := 1; i <= 2; i++ {
		if decision := scheduler.Book(request(t, "2026-06-15", "2026-06-18")); !decision.Booked {
			t.Fatalf("booking %d was refused: %s", i, decision.Reason)
		}
	}
	if got := scheduler.Booked("Northgate", day); got != 2 {
		t.Fatalf("Booked = %d, want 2", got)
	}
}

// TestReserveSlotIsEmergencyOnly walks a single day to its end: the routine
// appointments fill, the reserve absorbs exactly one emergency, and then the day
// is closed to everyone.
func TestReserveSlotIsEmergencyOnly(t *testing.T) {
	scheduler := northgate(t)
	day := mustDay(t, "2026-06-18")

	for i := 1; i <= 2; i++ {
		decision := scheduler.Book(request(t, "2026-06-15", "2026-06-18"))
		if !decision.Booked || decision.Slot != inspection.SlotRoutine {
			t.Fatalf("routine booking %d: booked=%t slot=%q reason=%q",
				i, decision.Booked, decision.Slot, decision.Reason)
		}
	}

	expedited := request(t, "2026-06-16", "2026-06-18")
	expedited.Priority = inspection.PriorityExpedited
	if decision := scheduler.Book(expedited); decision.Booked ||
		decision.Reason != inspection.ReasonNoCapacity {
		t.Fatalf("expedited request took a reserve slot: booked=%t reason=%q",
			decision.Booked, decision.Reason)
	}

	emergency := request(t, "2026-06-18", "2026-06-18")
	emergency.Priority = inspection.PriorityEmergency
	decision := scheduler.Book(emergency)
	if !decision.Booked || decision.Slot != inspection.SlotReserve {
		t.Fatalf("first emergency: booked=%t slot=%q reason=%q",
			decision.Booked, decision.Slot, decision.Reason)
	}
	if decision := scheduler.Book(emergency); decision.Booked ||
		decision.Reason != inspection.ReasonNoCapacity {
		t.Fatalf("second emergency reused the reserve slot: booked=%t reason=%q",
			decision.Booked, decision.Reason)
	}

	if got := scheduler.Booked("Northgate", day); got != 3 {
		t.Fatalf("Booked = %d, want 3", got)
	}
}

// TestCapacityIsPerDistrictPerDay proves the booking key is both dimensions. A
// key that dropped either one would silently ration a whole division to one
// district's worth of work.
func TestCapacityIsPerDistrictPerDay(t *testing.T) {
	scheduler := northgate(t)
	scheduler.SetCapacity("Eastbank", inspection.Capacity{Routine: 2, Reserve: 1})

	for i := 1; i <= 2; i++ {
		if decision := scheduler.Book(request(t, "2026-06-15", "2026-06-18")); !decision.Booked {
			t.Fatalf("Northgate booking %d was refused: %s", i, decision.Reason)
		}
	}

	nextDay := request(t, "2026-06-15", "2026-06-19")
	if decision := scheduler.Book(nextDay); !decision.Booked {
		t.Fatalf("a full day closed the day after it: %s", decision.Reason)
	}

	otherDistrict := request(t, "2026-06-15", "2026-06-18")
	otherDistrict.District = "Eastbank"
	if decision := scheduler.Book(otherDistrict); !decision.Booked {
		t.Fatalf("a full district closed its neighbor: %s", decision.Reason)
	}

	if got := scheduler.Booked("Eastbank", mustDay(t, "2026-06-18")); got != 1 {
		t.Fatalf("Eastbank Booked = %d, want 1", got)
	}
}
