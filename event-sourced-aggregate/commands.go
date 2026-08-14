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

// Fulfill releases responsive records and closes the request.
type Fulfill struct {
	ReleasedPages int
	At            time.Time
}

// Deny withholds records under a cited exemption and closes the request.
type Deny struct {
	Exemption string
	At        time.Time
}
