package acceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/edgentx/code-examples/records-service/httpapi"
	"github.com/edgentx/code-examples/records-service/requests"
)

// The console criteria drive the real HTTP handler over a real server, so what
// they assert is what a browser would receive: the status line, the entity tag,
// the problem document, and the list of actions the caller may take. Nothing is
// stubbed between the request and the event store.

// consoleState is what the console is holding: the request it has open, the
// version it read, and the last answer it got.
type consoleState struct {
	requestID string
	principal string
	version   int
	status    int
	etag      string
	view      requests.View
	problem   httpapi.Problem
	keys      int
}

// registerConsoleSteps adds the HTTP vocabulary to the same suite.
func (s *scenario) registerConsoleSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the console reads "([^"]*)" as "([^"]*)"$`, s.consoleReads)
	ctx.Step(`^the console has read "([^"]*)" as "([^"]*)"$`, s.consoleHasRead)
	ctx.Step(`^the console reads "([^"]*)" as nobody$`, s.consoleReadsAnonymously)
	ctx.Step(`^the console assigns "([^"]*)" at the version it read$`, s.consoleAssigns)
	ctx.Step(`^the console assigns "([^"]*)" with no idempotency key$`, s.consoleAssignsWithoutKey)
	ctx.Step(`^the console releases (\d+) pages at the version it read$`, s.consoleReleases)
	ctx.Step(`^the console was told 409 for its edit$`, s.consoleWasToldConflict)
	ctx.Step(`^"([^"]*)" assigns "([^"]*)" to "([^"]*)" meanwhile$`, s.assignsMeanwhile)

	ctx.Step(`^the console is told (\d+)$`, s.consoleTold)
	ctx.Step(`^the console is told (\d+) with code "([^"]*)"$`, s.consoleToldWithCode)
	ctx.Step(`^the entity tag is the version (\d+)$`, s.entityTagIs)
	ctx.Step(`^the console is offered "([^"]*)"$`, s.consoleOffered)
	ctx.Step(`^the conflict names the current version (\d+)$`, s.conflictNamesVersion)
}

// server starts the API on demand, so scenarios that never touch HTTP pay
// nothing for it.
func (s *scenario) server() *httptest.Server {
	if s.api == nil {
		s.api = httptest.NewServer(httpapi.New(s.office.Requests, nil).Handler())
	}
	return s.api
}

// call makes one request the way the console makes it and records the answer.
func (s *scenario) call(method, path, principal, key string, version int, body any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, s.server().URL+path, payload)
	if err != nil {
		return err
	}
	if principal != "" {
		request.Header.Set(httpapi.UserHeader, principal)
	}
	if key != "" {
		request.Header.Set(httpapi.IdempotencyHeader, key)
	}
	if version >= 0 {
		// The console always sends the version it showed the operator. That is
		// what turns an edit made against a stale screen into a refusal.
		request.Header.Set("If-Match", strconv.Quote(strconv.Itoa(version)))
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := s.server().Client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	s.console.status = response.StatusCode
	s.console.etag = response.Header.Get("ETag")
	s.console.view = requests.View{}
	s.console.problem = httpapi.Problem{}

	switch response.Header.Get("Content-Type") {
	case httpapi.ProblemContentType:
		if err := json.Unmarshal(answer, &s.console.problem); err != nil {
			return fmt.Errorf("the problem document did not decode: %w", err)
		}
	default:
		if len(bytes.TrimSpace(answer)) > 0 {
			if err := json.Unmarshal(answer, &s.console.view); err != nil {
				return fmt.Errorf("the response body did not decode: %w", err)
			}
		}
	}
	return nil
}

// consoleKey mints the idempotency key the console would attach to one edit.
func (s *scenario) consoleKey() string {
	s.console.keys++
	return fmt.Sprintf("console-%02d", s.console.keys)
}

func (s *scenario) consoleReads(requestID, who string) error {
	s.console.requestID = requestID
	s.console.principal = who
	if err := s.call(http.MethodGet, "/api/requests/"+requestID, who, "", -1, nil); err != nil {
		return err
	}
	if s.console.status == http.StatusOK {
		// This is the whole of the console's optimistic-locking state: the
		// version it was shown, sent back with the edit.
		s.console.version = s.console.view.Version
	}
	return nil
}

func (s *scenario) consoleHasRead(requestID, who string) error {
	if err := s.consoleReads(requestID, who); err != nil {
		return err
	}
	return s.consoleTold(http.StatusOK)
}

func (s *scenario) consoleReadsAnonymously(requestID string) error {
	s.console.requestID = requestID
	return s.call(http.MethodGet, "/api/requests/"+requestID, "", "", -1, nil)
}

func (s *scenario) consoleAssigns(reviewer string) error {
	return s.call(http.MethodPost, "/api/requests/"+s.console.requestID+"/reviewer",
		s.console.principal, s.consoleKey(), s.console.version,
		map[string]string{"reviewer": reviewer})
}

func (s *scenario) consoleAssignsWithoutKey(reviewer string) error {
	return s.call(http.MethodPost, "/api/requests/"+s.console.requestID+"/reviewer",
		s.console.principal, "", s.console.version,
		map[string]string{"reviewer": reviewer})
}

func (s *scenario) consoleReleases(pages int) error {
	return s.call(http.MethodPost, "/api/requests/"+s.console.requestID+"/release",
		s.console.principal, s.consoleKey(), s.console.version,
		map[string]int{"released_pages": pages})
}

// consoleWasToldConflict replays the refused edit so the retry scenario starts
// from the state the operator is actually in: an edit that has already failed.
func (s *scenario) consoleWasToldConflict() error {
	if err := s.consoleAssigns("records.officer.9"); err != nil {
		return err
	}
	return s.consoleToldWithCode(http.StatusConflict, "version_conflict")
}

// assignsMeanwhile is the other officer, working through the service while the
// console's screen goes stale.
func (s *scenario) assignsMeanwhile(who, reviewer, requestID string) error {
	return s.must(s.assigns(who, reviewer, requestID))
}

func (s *scenario) consoleTold(status int) error {
	if s.console.status != status {
		return fmt.Errorf("the console was told %d (%s), want %d",
			s.console.status, s.console.problem.Title, status)
	}
	return nil
}

func (s *scenario) consoleToldWithCode(status int, code string) error {
	if err := s.consoleTold(status); err != nil {
		return err
	}
	if s.console.problem.Code != code {
		return fmt.Errorf("the problem code is %q, want %q", s.console.problem.Code, code)
	}
	if s.console.problem.Status != status {
		return fmt.Errorf("the problem document says status %d, the response said %d",
			s.console.problem.Status, status)
	}
	if s.console.problem.Title == "" {
		return errors.New("the problem document has no title for the operator to read")
	}
	return nil
}

func (s *scenario) entityTagIs(version int) error {
	want := strconv.Quote(strconv.Itoa(version))
	if s.console.etag != want {
		return fmt.Errorf("the entity tag is %q, want %q", s.console.etag, want)
	}
	return nil
}

// consoleOffered checks the capability list the console renders its controls
// from. The console holds no rule of its own, so this list is the only thing
// standing between an operator and a button that would be refused.
func (s *scenario) consoleOffered(expected string) error {
	want := strings.Split(expected, ", ")
	got := s.console.view.AllowedActions
	if len(got) != len(want) {
		return fmt.Errorf("the console is offered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("the console is offered %v, want %v", got, want)
		}
	}
	return nil
}

func (s *scenario) conflictNamesVersion(version int) error {
	if s.console.problem.CurrentVersion == nil {
		return errors.New("the conflict does not say what the current version is, " +
			"so the operator has nothing to start again from")
	}
	if *s.console.problem.CurrentVersion != version {
		return fmt.Errorf("the conflict names version %d, want %d",
			*s.console.problem.CurrentVersion, version)
	}
	return nil
}
