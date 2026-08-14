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
	RequestID   string
	Requester   string
	Description string
	At          time.Time
	DueAt       time.Time
}

func (e Submitted) EventName() string     { return "records_request.submitted" }
func (e Submitted) OccurredAt() time.Time { return e.At }

// Acknowledged records the agency's receipt notice to the requester.
type Acknowledged struct {
	RequestID string
	At        time.Time
}

func (e Acknowledged) EventName() string     { return "records_request.acknowledged" }
func (e Acknowledged) OccurredAt() time.Time { return e.At }

// ReviewerAssigned names the records officer responsible for the response.
type ReviewerAssigned struct {
	RequestID string
	Reviewer  string
	At        time.Time
}

func (e ReviewerAssigned) EventName() string     { return "records_request.reviewer_assigned" }
func (e ReviewerAssigned) OccurredAt() time.Time { return e.At }

// Fulfilled closes the request by releasing responsive records.
type Fulfilled struct {
	RequestID     string
	ReleasedPages int
	At            time.Time
}

func (e Fulfilled) EventName() string     { return "records_request.fulfilled" }
func (e Fulfilled) OccurredAt() time.Time { return e.At }

// Denied closes the request by withholding records under a cited exemption.
type Denied struct {
	RequestID string
	Exemption string
	At        time.Time
}

func (e Denied) EventName() string     { return "records_request.denied" }
func (e Denied) OccurredAt() time.Time { return e.At }
