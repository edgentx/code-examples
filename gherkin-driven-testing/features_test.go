package inspection_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	inspection "github.com/edgentx/code-examples/gherkin-driven-testing"
)

// TestFeatures runs every file under features/ as part of the ordinary Go test
// suite. There is no separate binary and no separate command: `go test ./...`
// runs the acceptance criteria, so a criterion that stops holding breaks the
// build the same way a unit test does.
//
// TestingT is what makes that true. With it set, godog runs each scenario as a
// subtest of this one and reports failing steps through the *testing.T it was
// handed, instead of only returning a status code. Failures arrive as ordinary
// Go test output, and `go test -run 'TestFeatures/<scenario name>'` selects a
// single criterion the way it selects any other subtest.
func TestFeatures(t *testing.T) {
	// One scenario struct for the whole run, on purpose. It is the shape every
	// suite arrives at as soon as anything in it is expensive to build -- a test
	// server, a database handle, a fixture load -- and it is the shape in which
	// scenarios start leaking into each other. Isolation is therefore not a
	// property of where this variable was declared; it is the Before hook's job.
	state := &scenario{}

	suite := godog.TestSuite{
		ScenarioInitializer: state.register,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
			// Strict fails the run on a step nobody implemented. Without it an
			// undefined step is merely reported, and a criterion could sit in the
			// feature file for months being counted as delivered while never
			// having executed once.
			Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance criteria did not pass")
	}
}

// permitRecord is what a Given step told us about a permit.
type permitRecord struct {
	status   inspection.PermitStatus
	district string
}

// scenario is the whole world one scenario is allowed to see: the calendar and
// scheduler it acts on, the permits its Given steps described, and the single
// decision its When step produced.
type scenario struct {
	calendar  *inspection.Calendar
	scheduler *inspection.Scheduler
	permits   map[string]permitRecord

	decision inspection.Decision
	decided  bool
}

// errNoDecision guards a Then step that runs without a When step before it,
// which would otherwise assert against a zero-valued decision and pass.
var errNoDecision = errors.New("no inspection request has been filed in this scenario")

// register wires the Gherkin vocabulary to the step definitions and installs the
// per-scenario reset. godog calls it once for every scenario.
func (s *scenario) register(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	ctx.Step(`^the inspection calendar observes "([^"]*)" on (\d{4}-\d{2}-\d{2})$`, s.calendarObserves)
	ctx.Step(`^the "([^"]*)" district staffs (\d+) routine slots? and (\d+) reserve slots? each day$`, s.districtStaffs)
	ctx.Step(`^permit "([^"]*)" is (active|expired|suspended) in the "([^"]*)" district$`, s.permitStanding)
	ctx.Step(`^(\d+) routine inspections? (?:is|are) already booked in "([^"]*)" on (\d{4}-\d{2}-\d{2})$`, s.routineInspectionsBooked)
	ctx.Step(`^(\d+) emergency inspections? (?:is|are) already booked in "([^"]*)" on (\d{4}-\d{2}-\d{2})$`, s.emergencyInspectionsBooked)
	ctx.Step(`^an? "([^"]*)" inspection for permit "([^"]*)" is filed on (\d{4}-\d{2}-\d{2}) for (\d{4}-\d{2}-\d{2})$`, s.inspectionFiled)
	ctx.Step(`^the inspection is booked$`, s.inspectionIsBooked)
	ctx.Step(`^the booking uses a (routine|reserve) slot$`, s.bookingUsesSlot)
	ctx.Step(`^the request is refused because "([^"]*)"$`, s.requestRefusedBecause)
}

// reset returns the scenario to a world with no holidays, no districts, no
// permits, and no decision.
//
// This is not housekeeping, it is what lets a feature file be read as a
// contract. Every capacity scenario in daily_capacity.feature books onto
// 2026-06-18 in a district that staffs two appointments a day. Delete this one
// hook and five of the twenty scenarios fail on the spot, because each finds the
// day already committed by the scenario before it. That failure is the lucky
// case: a suite that leaks silently is no longer a set of independent statements
// about the system, it is a script that only means anything read top to bottom.
func (s *scenario) reset() {
	s.calendar = inspection.NewCalendar()
	s.scheduler = inspection.NewScheduler(s.calendar)
	s.permits = make(map[string]permitRecord)
	s.decision = inspection.Decision{}
	s.decided = false
}

func (s *scenario) calendarObserves(name, date string) error {
	day, err := parseDay(date)
	if err != nil {
		return err
	}
	s.calendar.Observe(day, name)
	return nil
}

func (s *scenario) districtStaffs(district string, routine, reserve int) error {
	s.scheduler.SetCapacity(district, inspection.Capacity{Routine: routine, Reserve: reserve})
	return nil
}

// permitStanding records what the feature said about a permit. The status word
// is cast rather than translated through a lookup table: the words in the
// feature file are the domain's own vocabulary, and keeping a second spelling of
// them here would be a place for the two to drift apart.
func (s *scenario) permitStanding(permitID, status, district string) error {
	s.permits[permitID] = permitRecord{
		status:   inspection.PermitStatus(status),
		district: district,
	}
	return nil
}

func (s *scenario) routineInspectionsBooked(count int, district, date string) error {
	day, err := parseDay(date)
	if err != nil {
		return err
	}
	// The precondition is reached the way real traffic reaches it, by booking
	// ordinary requests filed far enough ahead to clear the notice rule, rather
	// than by reaching into the scheduler's counters. A setup step that writes
	// state directly can arrange a situation the domain would never allow, and
	// the scenario then proves something about a world that cannot happen.
	return s.preload(count, inspection.Request{
		Permit:   inspection.PermitActive,
		District: district,
		Priority: inspection.PriorityStandard,
		FiledOn:  day.AddDate(0, 0, -30),
		WantedOn: day,
	})
}

func (s *scenario) emergencyInspectionsBooked(count int, district, date string) error {
	day, err := parseDay(date)
	if err != nil {
		return err
	}
	return s.preload(count, inspection.Request{
		Permit:   inspection.PermitActive,
		District: district,
		Priority: inspection.PriorityEmergency,
		FiledOn:  day,
		WantedOn: day,
	})
}

// preload books the same request count times and fails loudly if the domain
// refuses one, so a scenario can never run against a precondition that was not
// actually established.
func (s *scenario) preload(count int, template inspection.Request) error {
	for i := 1; i <= count; i++ {
		template.PermitID = fmt.Sprintf("BP-SETUP-%02d", i)
		if decision := s.scheduler.Book(template); !decision.Booked {
			return fmt.Errorf("could not set up booking %d of %d in %q: %s",
				i, count, template.District, decision.Reason)
		}
	}
	return nil
}

// inspectionFiled is the only step that exercises the system under test. It
// parses, calls, and stores the answer; it decides nothing.
func (s *scenario) inspectionFiled(priority, permitID, filed, wanted string) error {
	permit, described := s.permits[permitID]
	if !described {
		return fmt.Errorf("permit %q was never described by a Given step", permitID)
	}
	filedOn, err := parseDay(filed)
	if err != nil {
		return err
	}
	wantedOn, err := parseDay(wanted)
	if err != nil {
		return err
	}
	s.decision = s.scheduler.Book(inspection.Request{
		PermitID: permitID,
		Permit:   permit.status,
		District: permit.district,
		Priority: inspection.Priority(priority),
		FiledOn:  filedOn,
		WantedOn: wantedOn,
	})
	s.decided = true
	return nil
}

func (s *scenario) inspectionIsBooked() error {
	if !s.decided {
		return errNoDecision
	}
	if !s.decision.Booked {
		return fmt.Errorf("expected the inspection to be booked, but it was refused: %s", s.decision.Reason)
	}
	return nil
}

func (s *scenario) bookingUsesSlot(kind string) error {
	if !s.decided {
		return errNoDecision
	}
	if !s.decision.Booked {
		return fmt.Errorf("expected a %s slot, but the request was refused: %s", kind, s.decision.Reason)
	}
	if string(s.decision.Slot) != kind {
		return fmt.Errorf("expected a %s slot, got a %s slot", kind, s.decision.Slot)
	}
	return nil
}

func (s *scenario) requestRefusedBecause(reason string) error {
	if !s.decided {
		return errNoDecision
	}
	if s.decision.Booked {
		return fmt.Errorf("expected the refusal %q, but the inspection was booked into a %s slot",
			reason, s.decision.Slot)
	}
	if string(s.decision.Reason) != reason {
		return fmt.Errorf("expected the refusal %q, got %q", reason, s.decision.Reason)
	}
	return nil
}

// parseDay reads the plain calendar dates the feature files use.
func parseDay(date string) (time.Time, error) {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}, fmt.Errorf("feature file has an unreadable date %q: %w", date, err)
	}
	return day, nil
}
