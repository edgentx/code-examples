package recordsrequest

import "time"

// Commands are requests to change the aggregate. They are validated against the
// current state and either rejected or turned into events; they are never stored.

// Submit opens a new records request.
type Submit struct {
	RequestID   string
	Requester   string
	Description string
	At          time.Time
}

// Acknowledge sends the statutory receipt notice.
type Acknowledge struct {
	At time.Time
}

// AssignReviewer routes the request to a records officer.
type AssignReviewer struct {
	Reviewer string
	At       time.Time
}

// Fulfill releases responsive records for delivery.
type Fulfill struct {
	ReleasedPages int
	At            time.Time
}

// ConfirmDelivery closes the request once the release package has reached the
// requester. It is issued by the records service on behalf of the fulfillment
// service, which reports the outcome as a fact rather than calling back into
// the aggregate directly.
type ConfirmDelivery struct {
	PackageID string
	At        time.Time
}

// FailDelivery compensates a release that could not be delivered.
type FailDelivery struct {
	Reason string
	At     time.Time
}

// Deny withholds records under a cited exemption and closes the request.
type Deny struct {
	Exemption string
	At        time.Time
}
