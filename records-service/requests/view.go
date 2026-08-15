package requests

import (
	"strings"
	"time"

	"github.com/edgentx/code-examples/records-service/authz"
	"github.com/edgentx/code-examples/records-service/recordsrequest"
)

// View is what a caller sees. It is deliberately flat and deliberately small,
// and two of its fields carry the whole contract with the console:
//
//   - Status is the one lifecycle field. Everything the console shows about
//     where a request stands is derived from it. There is no second flag, no
//     "in flight" boolean and no "closed" boolean, because two fields that
//     describe the same thing eventually disagree and the screen then shows a
//     state the record was never in.
//   - AllowedActions is what this caller may do, decided by the authorization
//     model on the server. The console renders controls from it and holds no
//     rule of its own, so a control that appears is a control whose action will
//     be permitted, and changing the model changes the screen without a release.
//
// Version is what the caller sends back when it edits, which is how a stale edit
// becomes a conflict the operator can see rather than an overwrite nobody sees.
type View struct {
	ID             string    `json:"id"`
	Requester      string    `json:"requester"`
	Description    string    `json:"description"`
	Status         string    `json:"status"`
	Reviewer       string    `json:"reviewer"`
	SubmittedAt    time.Time `json:"submitted_at"`
	DueAt          time.Time `json:"due_at"`
	ReleasedPages  int       `json:"released_pages"`
	Exemption      string    `json:"exemption"`
	PackageID      string    `json:"package_id"`
	FailureCause   string    `json:"failure_cause"`
	Version        int       `json:"version"`
	AllowedActions []string  `json:"allowed_actions"`
}

// newView renders an aggregate and a set of permitted actions.
func newView(request *recordsrequest.Request, actions []authz.Action) View {
	allowed := make([]string, 0, len(actions))
	for _, action := range actions {
		allowed = append(allowed, ActionName(action))
	}
	return View{
		ID:             request.ID(),
		Requester:      request.Requester(),
		Description:    request.Description(),
		Status:         string(request.Status()),
		Reviewer:       request.Reviewer(),
		SubmittedAt:    request.SubmittedAt(),
		DueAt:          request.DueAt(),
		ReleasedPages:  request.ReleasedPages(),
		Exemption:      request.Exemption(),
		PackageID:      request.PackageID(),
		FailureCause:   request.FailureCause(),
		Version:        request.Version(),
		AllowedActions: allowed,
	}
}

// ActionName is the short name an action travels under outside the
// authorization model: "release" rather than "can_release". The model's spelling
// stays inside the model, and the console never has to know it.
func ActionName(action authz.Action) string {
	return strings.TrimPrefix(string(action), "can_")
}

// viewOfSummary renders a read-model row together with what this caller may do
// to it. The list view and the detail view therefore carry the same fields and
// the same capabilities, so opening a request cannot change what the console
// believes about it.
func viewOfSummary(summary recordsrequest.Summary, actions []authz.Action) View {
	allowed := make([]string, 0, len(actions))
	for _, action := range actions {
		allowed = append(allowed, ActionName(action))
	}
	return View{
		ID:             summary.RequestID,
		Requester:      summary.Requester,
		Description:    summary.Description,
		Status:         string(summary.Status),
		Reviewer:       summary.Reviewer,
		SubmittedAt:    summary.SubmittedAt,
		DueAt:          summary.DueAt,
		ReleasedPages:  summary.ReleasedPages,
		Exemption:      summary.Exemption,
		PackageID:      summary.PackageID,
		FailureCause:   summary.FailureCause,
		Version:        summary.Version,
		AllowedActions: allowed,
	}
}
