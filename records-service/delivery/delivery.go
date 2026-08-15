// Package delivery is the message contract between the records service and the
// fulfillment service: the event types one emits and the other applies, and the
// payload they carry.
//
// It exists as its own package because it is the only thing the two services are
// allowed to share. Neither imports the other -- that is what makes them
// separable -- so the shape of the fact they exchange has to live somewhere both
// can see, and it has to be small enough that changing it is obviously a change
// to an interface between systems.
package delivery

// Source is the CloudEvents `source` of the fulfillment service.
const Source = "/agency/fulfillment-service"

// The two facts the fulfillment service reports. One of them closes a request;
// the other compensates the release that produced it. Nothing else comes back,
// and there is no "in progress" fact: a caller that needs to know a release is
// out looks at the request's status, which already says so.
const (
	// TypeConfirmed says the release package reached the requester.
	TypeConfirmed = "records_delivery.confirmed"
	// TypeFailed says it did not, and will not, so the release must be undone.
	TypeFailed = "records_delivery.failed"
)

// Outcome is the payload of both facts.
type Outcome struct {
	RequestID string `json:"request_id"`
	// PackageID is set on a confirmation.
	PackageID string `json:"package_id,omitempty"`
	// Reason is set on a failure and is written for the records officer who has
	// to decide what to do next, not for a log.
	Reason string `json:"reason,omitempty"`
}

// MessageID derives the identity of an outcome message from the identity of the
// message that caused it.
//
// This is what makes the second hop of the choreography safe. The fulfillment
// service may process the same release twice -- a relay that crashed between
// publishing and marking will send it again -- and if it did, it would report
// the outcome twice. Deriving the outcome's id from the cause means both reports
// carry the same id, so the records service recognizes the second one as the
// same fact and applies it once.
func MessageID(causeMessageID string) string { return causeMessageID + "/delivery" }
