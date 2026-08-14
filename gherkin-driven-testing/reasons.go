package inspection

// Reason is why a request was refused.
//
// The values are the exact phrases the feature files quote. That is deliberate:
// the refusal a contractor is read over the phone, the string in the API
// response, and the acceptance criterion are one artifact. Renaming a rule here
// fails the feature file until the criterion is renegotiated with the people who
// agreed to it, which is the behavior we want from a contract.
type Reason string

const (
	// ReasonNone is the zero value, carried by a booked decision.
	ReasonNone Reason = ""
	// ReasonUnknownDistrict is refused when the address is outside the
	// jurisdiction this division inspects.
	ReasonUnknownDistrict Reason = "district is outside the inspection area"
	// ReasonPermitNotActive is refused when the permit has expired or has been
	// suspended, which is decided before the calendar is ever consulted.
	ReasonPermitNotActive Reason = "permit is not active"
	// ReasonUnknownPriority is refused when the priority is not one the division
	// publishes a notice period for.
	ReasonUnknownPriority Reason = "priority is not recognized"
	// ReasonDateInPast is refused when the day wanted is earlier than the day the
	// request was filed.
	ReasonDateInPast Reason = "requested date has already passed"
	// ReasonNotABusinessDay is refused when the day wanted is a weekend or an
	// observed holiday. No priority overrides this one: nobody is at work.
	ReasonNotABusinessDay Reason = "requested date is not a business day"
	// ReasonInsufficientNotice is refused when the notice given is shorter than
	// the priority requires.
	ReasonInsufficientNotice Reason = "notice period is too short"
	// ReasonNoCapacity is refused when the district has committed every inspector
	// it has for that day.
	ReasonNoCapacity Reason = "no inspection slot is available"
)
