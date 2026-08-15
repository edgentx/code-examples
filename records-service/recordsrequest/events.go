package recordsrequest

import "time"

// Event is one immutable fact about a records request. Events are the only
// record of what happened: aggregate state is always a fold over them, never a
// separately persisted row that could drift from the history.
type Event interface {
	// EventName is the stable identifier written to the event store. It is part
	// of the storage contract, so it never changes once an event is in production.
	EventName() string
	// OccurredAt is the domain time the fact became true.
	OccurredAt() time.Time
}

// Submitted opens a request and starts the statutory response clock.
type Submitted struct {
	RequestID   string    `json:"request_id"`
	Requester   string    `json:"requester"`
	Description string    `json:"description"`
	At          time.Time `json:"at"`
	DueAt       time.Time `json:"due_at"`
}

func (e Submitted) EventName() string     { return "records_request.submitted" }
func (e Submitted) OccurredAt() time.Time { return e.At }

// Acknowledged records the agency's receipt notice to the requester.
type Acknowledged struct {
	RequestID string    `json:"request_id"`
	At        time.Time `json:"at"`
}

func (e Acknowledged) EventName() string     { return "records_request.acknowledged" }
func (e Acknowledged) OccurredAt() time.Time { return e.At }

// ReviewerAssigned names the records officer responsible for the response.
type ReviewerAssigned struct {
	RequestID string    `json:"request_id"`
	Reviewer  string    `json:"reviewer"`
	At        time.Time `json:"at"`
}

func (e ReviewerAssigned) EventName() string     { return "records_request.reviewer_assigned" }
func (e ReviewerAssigned) OccurredAt() time.Time { return e.At }

// Fulfilled releases responsive records for delivery. It is the fact the
// fulfillment service reacts to, and it does not close the request: the package
// still has to be assembled and delivered before the agency has answered.
type Fulfilled struct {
	RequestID     string    `json:"request_id"`
	ReleasedPages int       `json:"released_pages"`
	At            time.Time `json:"at"`
}

func (e Fulfilled) EventName() string     { return "records_request.fulfilled" }
func (e Fulfilled) OccurredAt() time.Time { return e.At }

// DeliveryConfirmed closes the request: the release package reached the
// requester and the delivering service said so.
type DeliveryConfirmed struct {
	RequestID string    `json:"request_id"`
	PackageID string    `json:"package_id"`
	At        time.Time `json:"at"`
}

func (e DeliveryConfirmed) EventName() string     { return "records_request.delivery_confirmed" }
func (e DeliveryConfirmed) OccurredAt() time.Time { return e.At }

// ReleaseFailed is the compensating fact. Delivery could not be completed, so
// the release is undone: the page count is withdrawn, the reason is recorded
// where the officer will see it, and the request goes back to being workable.
type ReleaseFailed struct {
	RequestID string    `json:"request_id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

func (e ReleaseFailed) EventName() string     { return "records_request.release_failed" }
func (e ReleaseFailed) OccurredAt() time.Time { return e.At }

// Denied closes the request by withholding records under a cited exemption.
type Denied struct {
	RequestID string    `json:"request_id"`
	Exemption string    `json:"exemption"`
	At        time.Time `json:"at"`
}

func (e Denied) EventName() string     { return "records_request.denied" }
func (e Denied) OccurredAt() time.Time { return e.At }
