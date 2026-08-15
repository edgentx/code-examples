package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/choreography"
	"github.com/edgentx/code-examples/records-service/cloudevent"
	"github.com/edgentx/code-examples/records-service/delivery"
	"github.com/edgentx/code-examples/records-service/fulfillment"
	"github.com/edgentx/code-examples/records-service/memorystore"
	"github.com/edgentx/code-examples/records-service/outbox"
	"github.com/edgentx/code-examples/records-service/projector"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
	"github.com/edgentx/code-examples/records-service/requests"
)

// TestFeatures runs every file under ../features as part of the ordinary Go test
// suite.
//
// TestingT is what makes that true. With it set, godog runs each scenario as a
// subtest of this one and reports failing steps through the *testing.T it was
// handed, instead of only returning a status code. Failures arrive as ordinary
// Go test output, and `go test -run 'TestFeatures/<scenario name>'` selects a
// single criterion the way it selects any other subtest.
func TestFeatures(t *testing.T) {
	// One scenario struct for the whole run, on purpose. It is the shape every
	// suite arrives at as soon as anything in it is expensive to build, and it
	// is the shape in which scenarios start leaking into each other. Isolation
	// is therefore not a property of where this variable was declared; it is the
	// Before hook's job.
	state := &scenario{}

	suite := godog.TestSuite{
		ScenarioInitializer: state.register,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../features"},
			TestingT: t,
			// Strict fails the run on a step nobody implemented. Without it an
			// undefined step is merely reported, and a criterion could sit in
			// the feature file for months being counted as delivered while never
			// having executed once.
			Strict: true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance criteria did not pass")
	}
}

// day0 is the fixture epoch: every scenario starts here so a statutory deadline
// is the same date on every run.
var day0 = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

// officeID is the office every scenario files with.
const officeID = "midtown"

// scenario is the whole world one scenario is allowed to see.
type scenario struct {
	store     *memorystore.Store
	access    *authz.Memory
	office    *choreography.Office
	assembler *stubAssembler
	api       *httptest.Server

	// clockMu guards the clock, because the criteria that send several commands
	// at once send them from several goroutines.
	clockMu sync.Mutex
	clock   time.Time
	ids     []string
	keys    int

	// What the last command produced. Then steps assert on these.
	view requests.View
	err  error
	// repeat replays the last command under a new key, for the resubmission
	// criteria.
	repeat func(key string) (requests.View, error)
	// trace is the trace the last command ran in.
	trace cloudevent.SpanContext
	// applied counts how many of a batch of simultaneous commands took effect.
	applied int

	// read remembers the version a principal read, so a scenario can act on a
	// screen that has since gone stale.
	read map[string]int
	// snapshot holds the read model taken before it was discarded, and rebuilt
	// is the empty one caught up from the stream in its place.
	snapshot []recordsrequest.Summary
	rebuilt  *memorystore.Store
	// priorWrites is how many commands had written before a batch of
	// simultaneous ones, so the batch's own effect can be counted.
	priorWrites int

	// What the transport saw.
	relay     *outbox.Relay
	published map[string]int
	outcomes  []cloudevent.Envelope
	relayErr  error

	// The console's state for the HTTP criteria.
	console consoleState
}

// register wires the Gherkin vocabulary to the step definitions and installs the
// per-scenario reset. godog calls it once for every scenario.
func (s *scenario) register(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	ctx.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.api != nil {
			s.api.Close()
			s.api = nil
		}
		return ctx, err
	})

	// Who works where.
	ctx.Step(`^the "([^"]*)" records office employs "([^"]*)" as a clerk$`, s.employsClerk)
	ctx.Step(`^the "([^"]*)" records office employs "([^"]*)" as a records officer$`, s.employsOfficer)

	// Commands.
	ctx.Step(`^"([^"]*)" files request "([^"]*)" for "([^"]*)" describing "([^"]*)"$`, s.files)
	ctx.Step(`^"([^"]*)" filed "([^"]*)" through the public portal$`, s.filedThroughPortal)
	ctx.Step(`^"([^"]*)" acknowledges "([^"]*)"$`, s.acknowledges)
	ctx.Step(`^"([^"]*)" acknowledges "([^"]*)" with no idempotency key$`, s.acknowledgesWithoutKey)
	ctx.Step(`^"([^"]*)" acknowledges "([^"]*)" at version (\d+)$`, s.acknowledgesAtVersion)
	ctx.Step(`^"([^"]*)" assigns "([^"]*)" to "([^"]*)"$`, s.assigns)
	ctx.Step(`^"([^"]*)" assigns "([^"]*)" to "([^"]*)" at the version they read$`, s.assignsAtVersionRead)
	ctx.Step(`^"([^"]*)" reads "([^"]*)" again and assigns "([^"]*)" at that version$`, s.readsAgainAndAssigns)
	ctx.Step(`^"([^"]*)" releases (\d+) pages of "([^"]*)"$`, s.releases)
	ctx.Step(`^"([^"]*)" releases (\d+) pages of "([^"]*)" in a new trace$`, s.releasesInNewTrace)
	ctx.Step(`^"([^"]*)" denies "([^"]*)" citing "([^"]*)"$`, s.denies)
	ctx.Step(`^"([^"]*)" reports that the package for "([^"]*)" failed because "([^"]*)"$`, s.reportsFailure)
	ctx.Step(`^"([^"]*)" attempts to "([^"]*)" "([^"]*)"$`, s.attempts)

	// Preconditions expressed in one line.
	ctx.Step(`^request "([^"]*)" has been acknowledged and assigned to "([^"]*)"$`, s.acknowledgedAndAssigned)
	ctx.Step(`^request "([^"]*)" has a release awaiting delivery$`, s.releaseAwaitingDelivery)
	ctx.Step(`^"([^"]*)" has read "([^"]*)" at version (\d+)$`, s.hasRead)

	// Resubmission.
	ctx.Step(`^the same filing is sent again under the same idempotency key$`, s.sameKeyAgain)
	ctx.Step(`^the same command is sent again under the same idempotency key$`, s.sameKeyAgain)
	ctx.Step(`^the same command is sent again under a new idempotency key$`, s.newKeyAgain)
	ctx.Step(`^(\d+) copies of the same filing are sent at once$`, s.simultaneousFilings)
	ctx.Step(`^(\d+) officers assign a reviewer to "([^"]*)" from version (\d+) at once$`, s.simultaneousAssignments)

	// The relay and the delivering service.
	ctx.Step(`^the outbox is drained$`, s.drains)
	ctx.Step(`^the outbox is drained and fails$`, s.drainsAndFails)
	ctx.Step(`^every message so far has been published$`, s.drains)
	ctx.Step(`^the process dies before the relay runs$`, s.processDies)
	ctx.Step(`^the relay is restarted and drains$`, s.drains)
	ctx.Step(`^the relay publishes and then dies before recording the dispatch$`, s.relayDiesAfterPublishing)
	ctx.Step(`^no package can be assembled because "([^"]*)"$`, s.assemblyRefused)
	ctx.Step(`^packages can be assembled again$`, s.assemblyWorks)
	ctx.Step(`^the delivering service is temporarily unreachable$`, s.deliveryUnreachable)
	ctx.Step(`^the delivering service is reachable again$`, s.assemblyWorks)

	// Outcomes.
	ctx.Step(`^the command is accepted$`, s.commandAccepted)
	ctx.Step(`^the command is refused because "([^"]*)"$`, s.refusedBecause)
	ctx.Step(`^the command is refused because the caller is not authorized$`, s.refusedUnauthorized)
	ctx.Step(`^the command is refused because the record changed$`, s.refusedConflict)
	ctx.Step(`^request "([^"]*)" is "([^"]*)"$`, s.requestIs)
	ctx.Step(`^request "([^"]*)" is assigned to "([^"]*)"$`, s.requestAssignedTo)
	ctx.Step(`^request "([^"]*)" shows (\d+) released pages$`, s.requestShowsPages)
	ctx.Step(`^request "([^"]*)" notes "([^"]*)"$`, s.requestNotes)
	ctx.Step(`^request "([^"]*)" has (\d+) events?$`, s.requestHasEvents)
	ctx.Step(`^the office holds (\d+) requests?$`, s.officeHolds)
	ctx.Step(`^(\d+) of the (\d+) (?:copies|commands) was applied$`, s.appliedCount)
	ctx.Step(`^(\d+) messages? (?:is|are) waiting to be published$`, s.messagesWaiting)
	ctx.Step(`^the waiting message is a CloudEvent of type "([^"]*)"$`, s.waitingMessageType)
	ctx.Step(`^the waiting message carries a traceparent$`, s.waitingMessageTraced)
	ctx.Step(`^the release message is still waiting to be published$`, s.releaseStillWaiting)
	ctx.Step(`^the release message was delivered twice$`, s.releaseDeliveredTwice)
	ctx.Step(`^exactly (\d+) packages? (?:has|have) been assembled$`, s.packagesAssembled)
	ctx.Step(`^the delivery outcome belongs to the trace the release started$`, s.outcomeSameTrace)
	ctx.Step(`^the delivery outcome is a different span from the release$`, s.outcomeDifferentSpan)

	// Authorization.
	ctx.Step(`^"([^"]*)" may "([^"]*)" "([^"]*)"$`, s.mayDo)
	ctx.Step(`^"([^"]*)" may not "([^"]*)" "([^"]*)"$`, s.mayNotDo)
	ctx.Step(`^the fulfillment service may "([^"]*)" "([^"]*)"$`, s.serviceMayDo)
	ctx.Step(`^"([^"]*)" sees (\d+) requests?$`, s.sees)

	// The stream is the record.
	ctx.Step(`^the request rebuilt from the stream matches the one that wrote it$`, s.rebuildMatches)
	ctx.Step(`^the read model is discarded and rebuilt from the stream$`, s.rebuildReadModel)
	ctx.Step(`^the rebuilt read model matches the one that was discarded$`, s.rebuiltModelMatches)
	ctx.Step(`^each event of "([^"]*)" records a traceparent and an idempotency key$`, s.eventsCarryMetadata)
	ctx.Step(`^the two events of "([^"]*)" were caused by different commands$`, s.eventsFromDifferentCommands)

	s.registerConsoleSteps(ctx)
}

// reset returns the scenario to an empty office with nobody employed, nothing
// filed, and nothing published.
//
// This is not housekeeping. Every scenario in every feature file files
// "PRR-2026-0041" and expects to be the only writer of it; delete this hook and
// the suite stops being a set of independent statements about the system and
// becomes a script that only means anything read top to bottom.
func (s *scenario) reset() {
	s.store = memorystore.New()
	s.access = authz.NewMemory()
	s.assembler = &stubAssembler{next: 1}
	s.clock = day0
	s.ids = nil
	s.keys = 0
	s.view = requests.View{}
	s.err = nil
	s.repeat = nil
	s.trace = cloudevent.SpanContext{}
	s.applied = 0
	s.read = make(map[string]int)
	s.snapshot = nil
	s.rebuilt = nil
	s.priorWrites = 0
	s.published = make(map[string]int)
	s.outcomes = nil
	s.relayErr = nil
	s.console = consoleState{}

	s.office = choreography.Wire(choreography.Config{
		Repo:      s.store,
		Model:     s.store,
		Access:    s.access,
		OfficeID:  officeID,
		Assembler: s.assembler,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options: []requests.Option{
			// A clock that steps forward one minute per command, so events are
			// ordered and their times are still predictable.
			requests.WithClock(func() time.Time {
				s.clockMu.Lock()
				defer s.clockMu.Unlock()
				s.clock = s.clock.Add(time.Minute)
				return s.clock
			}),
			requests.WithIDs(s.nextID),
		},
	})

	// A spy on the two facts the delivering service reports, so a criterion can
	// assert what came back without reaching into either service.
	record := func(_ context.Context, envelope cloudevent.Envelope) error {
		s.outcomes = append(s.outcomes, envelope)
		return nil
	}
	s.office.Bus.Subscribe(delivery.TypeConfirmed, record)
	s.office.Bus.Subscribe(delivery.TypeFailed, record)

	// Every ordinary relay pass publishes through a counter, so a criterion can
	// say how many times a message went out without either service knowing it is
	// being watched.
	s.relay = s.office.RelayThrough(
		&countingPublisher{inner: s.office.Bus, published: s.published},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := s.access.Write(context.Background(),
		choreography.Staff(officeID, nil, nil)); err != nil {
		panic(err)
	}
}

// nextID hands out the identifiers the feature files name, so a scenario can
// talk about "PRR-2026-0041" and mean the request it just filed.
func (s *scenario) nextID() string {
	if len(s.ids) == 0 {
		return fmt.Sprintf("PRR-2026-%04d", 9000+s.keys)
	}
	id := s.ids[0]
	s.ids = s.ids[1:]
	return id
}

// key mints a fresh idempotency key.
func (s *scenario) key() string {
	s.keys++
	return fmt.Sprintf("key-%02d", s.keys)
}

// --- Staffing ---------------------------------------------------------------

func (s *scenario) employsClerk(office, who string) error {
	return s.access.Write(context.Background(), choreography.Staff(office, []string{who}, nil))
}

func (s *scenario) employsOfficer(office, who string) error {
	return s.access.Write(context.Background(), choreography.Staff(office, nil, []string{who}))
}

// --- Commands ---------------------------------------------------------------

// act runs one command, remembers what it produced, and returns nil. A step that
// exercises the system never fails the scenario itself; the Then steps decide
// whether the outcome was the one the criterion asked for.
func (s *scenario) act(run func(cmd requests.Command) (requests.View, error), principal string,
	expected int) error {
	span, err := cloudevent.StartTrace()
	if err != nil {
		return err
	}
	s.trace = span
	key := s.key()
	command := func(key string) (requests.View, error) {
		return run(requests.Command{
			Principal:       authz.UserPrincipal(principal),
			IdempotencyKey:  key,
			ExpectedVersion: expected,
			Trace:           span,
		})
	}
	s.repeat = command
	s.view, s.err = command(key)
	return nil
}

// must runs a command that a Given step relies on and fails the scenario if the
// service refused it, so no criterion runs against a precondition that was never
// established.
func (s *scenario) must(err error) error {
	if err != nil {
		return err
	}
	return s.err
}

func (s *scenario) files(who, requestID, requester, description string) error {
	s.ids = append(s.ids, requestID)
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Submit(context.Background(), cmd, requests.Submission{
			Requester:   requester,
			Description: description,
		})
	}, who, requests.AnyVersion)
}

func (s *scenario) filedThroughPortal(who, requestID string) error {
	s.ids = append(s.ids, requestID)
	// The portal account is the requester; the clerk who logs it is not. That
	// distinction is what makes the requester's read a relationship rather than
	// a coincidence.
	return s.must(s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Submit(context.Background(), cmd, requests.Submission{
			RequesterPrincipal: authz.UserPrincipal(who),
			Requester:          strings.ToUpper(who[:1]) + who[1:],
			Description:        "records filed through the public portal",
		})
	}, "c.hall", requests.AnyVersion))
}

func (s *scenario) acknowledges(who, requestID string) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Acknowledge(context.Background(), cmd, requestID)
	}, who, requests.AnyVersion)
}

func (s *scenario) acknowledgesAtVersion(who, requestID string, version int) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Acknowledge(context.Background(), cmd, requestID)
	}, who, version)
}

func (s *scenario) acknowledgesWithoutKey(who, requestID string) error {
	s.view, s.err = s.office.Requests.Acknowledge(context.Background(), requests.Command{
		Principal:       authz.UserPrincipal(who),
		ExpectedVersion: requests.AnyVersion,
	}, requestID)
	return nil
}

func (s *scenario) assigns(who, reviewer, requestID string) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.AssignReviewer(context.Background(), cmd, requestID, reviewer)
	}, who, requests.AnyVersion)
}

func (s *scenario) assignsAtVersionRead(who, reviewer, requestID string) error {
	version, read := s.read[who]
	if !read {
		return fmt.Errorf("%q has not read %s in this scenario", who, requestID)
	}
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.AssignReviewer(context.Background(), cmd, requestID, reviewer)
	}, who, version)
}

func (s *scenario) readsAgainAndAssigns(who, requestID, reviewer string) error {
	if err := s.hasRead(who, requestID, 0); err != nil {
		return err
	}
	return s.assignsAtVersionRead(who, reviewer, requestID)
}

func (s *scenario) releases(who string, pages int, requestID string) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Release(context.Background(), cmd, requestID, pages)
	}, who, requests.AnyVersion)
}

func (s *scenario) releasesInNewTrace(who string, pages int, requestID string) error {
	return s.releases(who, pages, requestID)
}

func (s *scenario) denies(who, requestID, exemption string) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.Deny(context.Background(), cmd, requestID, exemption)
	}, who, requests.AnyVersion)
}

func (s *scenario) reportsFailure(who, requestID, reason string) error {
	return s.act(func(cmd requests.Command) (requests.View, error) {
		return s.office.Requests.FailDelivery(context.Background(), cmd, requestID, reason)
	}, who, requests.AnyVersion)
}

// attempts runs whichever action the outline names, with arguments that would be
// valid if the caller were permitted. Only the authorization answer is under
// test, so the arguments must never be the reason a step failed.
func (s *scenario) attempts(who, action, requestID string) error {
	switch action {
	case "read":
		s.view, s.err = s.office.Requests.View(context.Background(),
			authz.UserPrincipal(who), requestID)
		return nil
	case "acknowledge":
		return s.acknowledges(who, requestID)
	case "assign":
		return s.assigns(who, "records.officer.9", requestID)
	case "release":
		return s.releases(who, 18, requestID)
	case "deny":
		return s.denies(who, requestID, "personnel file exemption")
	case "record_delivery":
		return s.reportsFailure(who, requestID, "wrong address")
	default:
		return fmt.Errorf("no such action %q", action)
	}
}

// --- Preconditions ----------------------------------------------------------

func (s *scenario) acknowledgedAndAssigned(requestID, reviewer string) error {
	// A Background may have established this already, and a scenario may then
	// restate it as the starting point of its own Given. Establishing a
	// precondition that already holds must be a no-op, not a second filing.
	if _, err := s.store.Load(context.Background(), requestID); err == nil {
		return nil
	}
	if err := s.must(s.files("c.hall", requestID, "M. Alvarez", "bridge inspection reports")); err != nil {
		return err
	}
	if err := s.must(s.acknowledges("c.hall", requestID)); err != nil {
		return err
	}
	return s.must(s.assigns("r.okafor", reviewer, requestID))
}

func (s *scenario) releaseAwaitingDelivery(requestID string) error {
	if err := s.acknowledgedAndAssigned(requestID, "records.officer.7"); err != nil {
		return err
	}
	request, err := s.store.Load(context.Background(), requestID)
	if err != nil {
		return err
	}
	if request.Status() == recordsrequest.StatusReleasePending {
		return nil
	}
	// Released but not yet drained: the message is queued and the request is
	// waiting to hear what happened to the package.
	return s.must(s.releases("r.okafor", 18, requestID))
}

func (s *scenario) hasRead(who, requestID string, _ int) error {
	view, err := s.office.Requests.View(context.Background(), authz.UserPrincipal(who), requestID)
	if err != nil {
		return err
	}
	s.read[who] = view.Version
	return nil
}

// --- Resubmission -----------------------------------------------------------

func (s *scenario) sameKeyAgain() error {
	if s.repeat == nil {
		return errors.New("no command has been sent in this scenario")
	}
	// The key of the command that just ran, replayed exactly as a retrying
	// browser would replay it.
	s.view, s.err = s.repeat(fmt.Sprintf("key-%02d", s.keys))
	return nil
}

func (s *scenario) newKeyAgain() error {
	if s.repeat == nil {
		return errors.New("no command has been sent in this scenario")
	}
	s.view, s.err = s.repeat(s.key())
	return nil
}

func (s *scenario) simultaneousFilings(copies int) error {
	s.ids = append(s.ids, "PRR-2026-0041")
	key := s.key()
	return s.simultaneously(copies, func() (requests.View, error) {
		return s.office.Requests.Submit(context.Background(), requests.Command{
			Principal:       authz.UserPrincipal("c.hall"),
			IdempotencyKey:  key,
			ExpectedVersion: requests.AnyVersion,
		}, requests.Submission{
			Requester:   "M. Alvarez",
			Description: "bridge inspection reports",
		})
	})
}

func (s *scenario) simultaneousAssignments(officers int, requestID string, version int) error {
	keys := make([]string, officers)
	for i := range keys {
		keys[i] = s.key()
	}
	var next int
	var mu sync.Mutex
	return s.simultaneously(officers, func() (requests.View, error) {
		mu.Lock()
		i := next
		next++
		mu.Unlock()
		return s.office.Requests.AssignReviewer(context.Background(), requests.Command{
			Principal:       authz.UserPrincipal("r.okafor"),
			IdempotencyKey:  keys[i],
			ExpectedVersion: version,
		}, requestID, fmt.Sprintf("records.officer.%d", i+20))
	})
}

// simultaneously releases every caller at once and counts how many took effect.
func (s *scenario) simultaneously(callers int, attempt func() (requests.View, error)) error {
	s.priorWrites = s.distinctCommands()
	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		mu      sync.Mutex
		applied int
		lastErr error
	)
	start.Add(1)
	for range callers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			view, err := attempt()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				lastErr = err
				return
			}
			// A caller whose command was applied is the one whose version came
			// back one higher than the version everybody decided against.
			applied++
			s.view = view
		}()
	}
	start.Done()
	done.Wait()

	// Applied is counted from the store rather than from what each caller was
	// told: a replayed result is a success for the caller and must not count as
	// a second application.
	s.applied = s.appliedFromStore(callers)
	s.err = lastErr
	return nil
}

// appliedFromStore counts how many of the simultaneous commands actually wrote.
// It counts from the store rather than from what each caller was told, because a
// replayed result is a success for the caller and must not count as a second
// application. Every event records the key of the command that caused it, so the
// number of distinct keys on the stream is the number of commands that wrote.
func (s *scenario) appliedFromStore(int) int {
	return s.distinctCommands() - s.priorWrites
}

// distinctCommands counts the commands that have written to the store.
func (s *scenario) distinctCommands() int {
	stream, err := s.store.Stream(context.Background(), 0, 1000)
	if err != nil {
		return 0
	}
	keys := make(map[string]bool)
	for _, stored := range stream {
		keys[stored.Metadata.IdempotencyKey] = true
	}
	return len(keys)
}

// --- The relay and the delivering service -----------------------------------

func (s *scenario) drains() error {
	_, s.relayErr = s.relay.Drain(context.Background())
	if s.relayErr != nil {
		return s.relayErr
	}
	return nil
}

func (s *scenario) drainsAndFails() error {
	_, s.relayErr = s.relay.Drain(context.Background())
	if s.relayErr == nil {
		return errors.New("the relay pass was expected to fail and did not")
	}
	return nil
}

func (s *scenario) processDies() error { return nil }

// relayDiesAfterPublishing is the crash the outbox exists for: the message is
// out, and the process stops before the dispatch is recorded.
func (s *scenario) relayDiesAfterPublishing() error {
	crashing := &crashAfterPublish{
		inner:     s.office.Bus,
		published: s.published,
		// The relay dies on the release, after the far side has it. Earlier
		// messages go out and are marked normally, which is what makes the
		// republished one afterwards unambiguous.
		crashOn: "records_request.fulfilled",
	}
	relay := s.office.RelayThrough(crashing, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := relay.Drain(context.Background()); err == nil {
		return errors.New("the relay was expected to die and did not")
	}
	return nil
}

func (s *scenario) assemblyRefused(reason string) error {
	s.assembler.refusal = reason
	s.assembler.transient = false
	return nil
}

func (s *scenario) assemblyWorks() error {
	s.assembler.refusal = ""
	s.assembler.transient = false
	return nil
}

func (s *scenario) deliveryUnreachable() error {
	s.assembler.transient = true
	return nil
}

// --- Outcomes ---------------------------------------------------------------

func (s *scenario) commandAccepted() error {
	if s.err != nil {
		return fmt.Errorf("expected the command to be accepted, got: %v", s.err)
	}
	return nil
}

func (s *scenario) refusedBecause(reason string) error {
	if s.err == nil {
		return fmt.Errorf("expected the refusal %q, but the command was accepted", reason)
	}
	if !strings.Contains(s.err.Error(), reason) {
		return fmt.Errorf("expected the refusal %q, got %q", reason, s.err.Error())
	}
	return nil
}

func (s *scenario) refusedUnauthorized() error {
	if !errors.Is(s.err, authz.ErrDenied) {
		return fmt.Errorf("expected an authorization refusal, got: %v", s.err)
	}
	return nil
}

func (s *scenario) refusedConflict() error {
	if !errors.Is(s.err, recordsrequest.ErrVersionConflict) {
		return fmt.Errorf("expected a version conflict, got: %v", s.err)
	}
	return nil
}

func (s *scenario) requestIs(requestID, status string) error {
	request, err := s.store.Load(context.Background(), requestID)
	if err != nil {
		return err
	}
	if string(request.Status()) != status {
		return fmt.Errorf("%s is %q, want %q", requestID, request.Status(), status)
	}
	return nil
}

func (s *scenario) requestAssignedTo(requestID, reviewer string) error {
	request, err := s.store.Load(context.Background(), requestID)
	if err != nil {
		return err
	}
	if request.Reviewer() != reviewer {
		return fmt.Errorf("%s is assigned to %q, want %q", requestID, request.Reviewer(), reviewer)
	}
	return nil
}

func (s *scenario) requestShowsPages(requestID string, pages int) error {
	request, err := s.store.Load(context.Background(), requestID)
	if err != nil {
		return err
	}
	if request.ReleasedPages() != pages {
		return fmt.Errorf("%s shows %d released page(s), want %d",
			requestID, request.ReleasedPages(), pages)
	}
	return nil
}

func (s *scenario) requestNotes(requestID, note string) error {
	request, err := s.store.Load(context.Background(), requestID)
	if err != nil {
		return err
	}
	if request.FailureCause() != note {
		return fmt.Errorf("%s notes %q, want %q", requestID, request.FailureCause(), note)
	}
	return nil
}

func (s *scenario) requestHasEvents(requestID string, want int) error {
	stream, err := s.streamOf(requestID)
	if err != nil {
		return err
	}
	if len(stream) != want {
		return fmt.Errorf("%s has %d event(s), want %d", requestID, len(stream), want)
	}
	return nil
}

func (s *scenario) officeHolds(want int) error {
	if _, err := s.office.Projector.CatchUp(context.Background()); err != nil {
		return err
	}
	summaries, err := s.store.Summaries(context.Background())
	if err != nil {
		return err
	}
	if len(summaries) != want {
		return fmt.Errorf("the office holds %d request(s), want %d", len(summaries), want)
	}
	return nil
}

func (s *scenario) appliedCount(want, _ int) error {
	if s.applied != want {
		return fmt.Errorf("%d command(s) were applied, want %d", s.applied, want)
	}
	return nil
}

func (s *scenario) messagesWaiting(want int) error {
	pending, err := s.store.PendingOutbox(context.Background(), 100)
	if err != nil {
		return err
	}
	if len(pending) != want {
		return fmt.Errorf("%d message(s) are waiting, want %d", len(pending), want)
	}
	return nil
}

func (s *scenario) waitingMessageType(want string) error {
	pending, err := s.store.PendingOutbox(context.Background(), 1)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return errors.New("no message is waiting")
	}
	envelope := outbox.Message(pending[0])
	if err := envelope.Validate(); err != nil {
		return err
	}
	if envelope.Type != want {
		return fmt.Errorf("the waiting message is %q, want %q", envelope.Type, want)
	}
	return nil
}

func (s *scenario) waitingMessageTraced() error {
	pending, err := s.store.PendingOutbox(context.Background(), 1)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return errors.New("no message is waiting")
	}
	if _, found := outbox.Message(pending[0]).Span(); !found {
		return errors.New("the waiting message carries no readable traceparent")
	}
	return nil
}

func (s *scenario) releaseStillWaiting() error {
	pending, err := s.store.PendingOutbox(context.Background(), 100)
	if err != nil {
		return err
	}
	for _, stored := range pending {
		if stored.Name == "records_request.fulfilled" {
			return nil
		}
	}
	return errors.New("the release message is not waiting to be published")
}

func (s *scenario) releaseDeliveredTwice() error {
	stream, err := s.streamOf("PRR-2026-0041")
	if err != nil {
		return err
	}
	for _, stored := range stream {
		if stored.Name != "records_request.fulfilled" {
			continue
		}
		id := outbox.MessageID(stored.RequestID, stored.Version)
		if s.published[id] < 2 {
			return fmt.Errorf("the release message %s was published %d time(s), want at least twice",
				id, s.published[id])
		}
		return nil
	}
	return errors.New("no release has been recorded in this scenario")
}

func (s *scenario) packagesAssembled(want int) error {
	if got := s.office.Fulfillment.Assemblies(); got != want {
		return fmt.Errorf("%d package(s) were assembled, want %d", got, want)
	}
	return nil
}

func (s *scenario) outcomeSameTrace() error {
	outcome, err := s.lastOutcome()
	if err != nil {
		return err
	}
	span, found := outcome.Span()
	if !found {
		return errors.New("the delivery outcome carries no readable traceparent")
	}
	if span.TraceIDString() != s.trace.TraceIDString() {
		return fmt.Errorf("the outcome is in trace %s, want the release's %s",
			span.TraceIDString(), s.trace.TraceIDString())
	}
	return nil
}

func (s *scenario) outcomeDifferentSpan() error {
	outcome, err := s.lastOutcome()
	if err != nil {
		return err
	}
	span, _ := outcome.Span()
	if span.SpanIDString() == s.trace.SpanIDString() {
		return errors.New("the outcome reused the release's span, so the hops cannot be told apart")
	}
	return nil
}

func (s *scenario) lastOutcome() (cloudevent.Envelope, error) {
	if len(s.outcomes) == 0 {
		return cloudevent.Envelope{}, errors.New("the delivering service reported nothing")
	}
	return s.outcomes[len(s.outcomes)-1], nil
}

// --- Authorization ----------------------------------------------------------

func (s *scenario) mayDo(who, action, requestID string) error {
	allowed, err := s.allowed(authz.UserPrincipal(who), action, requestID)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%q may not %q %s, but the criterion says they may", who, action, requestID)
	}
	return nil
}

func (s *scenario) mayNotDo(who, action, requestID string) error {
	allowed, err := s.allowed(authz.UserPrincipal(who), action, requestID)
	if err != nil {
		return err
	}
	if allowed {
		return fmt.Errorf("%q may %q %s, but the criterion says they may not", who, action, requestID)
	}
	return nil
}

func (s *scenario) serviceMayDo(action, requestID string) error {
	allowed, err := s.allowed(
		authz.ServicePrincipal(choreography.FulfillmentServiceID), action, requestID)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("the fulfillment service may not %q %s", action, requestID)
	}
	return nil
}

func (s *scenario) allowed(principal, action, requestID string) (bool, error) {
	relation, known := map[string]authz.Action{
		"read":            authz.ActionRead,
		"acknowledge":     authz.ActionAcknowledge,
		"assign":          authz.ActionAssignReviewer,
		"release":         authz.ActionRelease,
		"deny":            authz.ActionDeny,
		"record_delivery": authz.ActionRecordDelivery,
	}[action]
	if !known {
		return false, fmt.Errorf("no such action %q", action)
	}
	return s.access.Allowed(context.Background(), principal, relation,
		authz.RequestObject(requestID))
}

func (s *scenario) sees(who string, want int) error {
	views, err := s.office.Requests.List(context.Background(), authz.UserPrincipal(who))
	if err != nil {
		return err
	}
	if len(views) != want {
		return fmt.Errorf("%q sees %d request(s), want %d", who, len(views), want)
	}
	return nil
}

// --- The stream is the record -----------------------------------------------

// rebuildMatches compares two renderings of the same request: the one the read
// model holds, built as the events arrived, and the one produced by replaying
// the stream from nothing. If they can differ, the stream is not the record.
func (s *scenario) rebuildMatches() error {
	ctx := context.Background()
	if _, err := s.office.Projector.CatchUp(ctx); err != nil {
		return err
	}
	projected, err := s.store.Summaries(ctx)
	if err != nil {
		return err
	}
	if len(projected) != 1 {
		return fmt.Errorf("the office holds %d request(s), want 1", len(projected))
	}

	rehydrated, err := s.store.Load(ctx, projected[0].RequestID)
	if err != nil {
		return err
	}
	if replayed := recordsrequest.SummaryOf(rehydrated); replayed != projected[0] {
		return fmt.Errorf("replayed %+v, but the writer left %+v", replayed, projected[0])
	}
	return nil
}

func (s *scenario) rebuildReadModel() error {
	ctx := context.Background()
	if _, err := s.office.Projector.CatchUp(ctx); err != nil {
		return err
	}
	before, err := s.store.Summaries(ctx)
	if err != nil {
		return err
	}
	s.snapshot = before

	// An empty read model, caught up from the same stream. Nothing else is
	// consulted, which is the point: the model holds nothing that is not
	// derivable from the events.
	rebuilt := memorystore.New()
	if _, err := projector.New(s.store, rebuilt, 0, nil).CatchUp(ctx); err != nil {
		return err
	}
	s.rebuilt = rebuilt
	return nil
}

func (s *scenario) rebuiltModelMatches() error {
	if s.rebuilt == nil {
		return errors.New("the read model has not been rebuilt in this scenario")
	}
	after, err := s.rebuilt.Summaries(context.Background())
	if err != nil {
		return err
	}
	if len(after) != len(s.snapshot) {
		return fmt.Errorf("the rebuilt model holds %d row(s), the discarded one held %d",
			len(after), len(s.snapshot))
	}
	for i := range after {
		if after[i] != s.snapshot[i] {
			return fmt.Errorf("row %d differs: rebuilt %+v, discarded %+v",
				i, after[i], s.snapshot[i])
		}
	}
	return nil
}

func (s *scenario) eventsCarryMetadata(requestID string) error {
	stream, err := s.streamOf(requestID)
	if err != nil {
		return err
	}
	for _, stored := range stream {
		if stored.Metadata.TraceParent == "" {
			return fmt.Errorf("%s v%d records no traceparent", requestID, stored.Version)
		}
		if stored.Metadata.IdempotencyKey == "" {
			return fmt.Errorf("%s v%d records no idempotency key", requestID, stored.Version)
		}
	}
	return nil
}

func (s *scenario) eventsFromDifferentCommands(requestID string) error {
	stream, err := s.streamOf(requestID)
	if err != nil {
		return err
	}
	if len(stream) != 2 {
		return fmt.Errorf("%s has %d event(s), want 2", requestID, len(stream))
	}
	if stream[0].Metadata.IdempotencyKey == stream[1].Metadata.IdempotencyKey {
		return errors.New("both events record the same idempotency key")
	}
	return nil
}

// streamOf reads one request's events out of the store.
func (s *scenario) streamOf(requestID string) ([]recordsrequest.Stored, error) {
	all, err := s.store.Stream(context.Background(), 0, 1000)
	if err != nil {
		return nil, err
	}
	stream := make([]recordsrequest.Stored, 0, len(all))
	for _, stored := range all {
		if stored.RequestID == requestID {
			stream = append(stream, stored)
		}
	}
	return stream, nil
}

// --- Doubles ----------------------------------------------------------------

// stubAssembler stands in for the document repository and the delivery channel.
// It is the only part of the fulfillment service replaced in the acceptance
// criteria, because it is the only part whose real behavior would make an
// assertion depend on something outside the example.
type stubAssembler struct {
	mu        sync.Mutex
	next      int
	refusal   string
	transient bool
}

func (a *stubAssembler) Assemble(_ context.Context, _ string, _ int) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.transient {
		return "", errors.New("the delivering service is unreachable")
	}
	if a.refusal != "" {
		return "", fulfillment.Refusal{Reason: a.refusal}
	}
	id := fmt.Sprintf("PKG-2026-%04d", a.next)
	a.next++
	return id, nil
}

// crashAfterPublish delivers the message and then reports that the relay died,
// which is the one ordering the outbox has to survive: the far side has the
// message, and this side never recorded that it sent it.
type crashAfterPublish struct {
	inner     outbox.Publisher
	published map[string]int
	crashOn   string
}

func (p *crashAfterPublish) Publish(ctx context.Context, envelope cloudevent.Envelope) error {
	p.published[envelope.ID]++
	if err := p.inner.Publish(ctx, envelope); err != nil {
		return err
	}
	if envelope.Type == p.crashOn {
		return errors.New("the relay process died before recording the dispatch")
	}
	return nil
}

// countingPublisher records every publish so a criterion can say how many times
// a message went out.
type countingPublisher struct {
	inner     outbox.Publisher
	published map[string]int
}

func (p *countingPublisher) Publish(ctx context.Context, envelope cloudevent.Envelope) error {
	p.published[envelope.ID]++
	return p.inner.Publish(ctx, envelope)
}
