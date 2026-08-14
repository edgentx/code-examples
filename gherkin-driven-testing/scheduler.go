// Package inspection decides whether a building permit inspection can be booked
// on the day a contractor wants it.
//
// The rules are the interesting part: how much notice each priority owes, which
// days the office is open, how many inspectors a district can commit in a day,
// and -- the rule people actually argue about -- which refusal a request that
// breaks two rules at once is given. Those rules are stated once, in
// features/*.feature, and this package is what makes them true.
package inspection

import "time"

// Priority is how urgent the requested inspection is. It buys a shorter notice
// period and, for an emergency, a slot held back on an otherwise full day. It
// never buys a day the office is closed.
type Priority string

const (
	// PriorityStandard is ordinary scheduled work.
	PriorityStandard Priority = "standard"
	// PriorityExpedited is work a contractor has paid to move up.
	PriorityExpedited Priority = "expedited"
	// PriorityEmergency is a life-safety condition: same-day service.
	PriorityEmergency Priority = "emergency"
)

// noticeRequired is the minimum business days of notice each priority owes.
//
// It is read with the comma-ok form rather than relying on a map's zero value,
// because the zero value here is "no notice required at all". A priority nobody
// has published a rule for must be refused, not silently granted the best
// service the division offers.
var noticeRequired = map[Priority]int{
	PriorityStandard:  3,
	PriorityExpedited: 1,
	PriorityEmergency: 0,
}

// PermitStatus is the standing of the permit the inspection is requested
// against.
type PermitStatus string

const (
	// PermitActive is a permit in good standing.
	PermitActive PermitStatus = "active"
	// PermitExpired is a permit past its term.
	PermitExpired PermitStatus = "expired"
	// PermitSuspended is a permit stopped by an enforcement action.
	PermitSuspended PermitStatus = "suspended"
)

// SlotKind is which of a district's two pools of daily appointments a booking
// consumed.
type SlotKind string

const (
	// SlotNone is the zero value, carried by a refused decision.
	SlotNone SlotKind = ""
	// SlotRoutine is one of the ordinary appointments in a district's day.
	SlotRoutine SlotKind = "routine"
	// SlotReserve is the appointment held back for emergencies.
	SlotReserve SlotKind = "reserve"
)

// Capacity is how many inspections one district can absorb in one day.
type Capacity struct {
	// Routine is the number of appointments open to any priority.
	Routine int
	// Reserve is the number held back so that an emergency can still be seen on
	// a day whose routine appointments are all committed.
	Reserve int
}

// Request is one contractor asking for one inspection on one day.
type Request struct {
	PermitID string
	Permit   PermitStatus
	District string
	Priority Priority
	FiledOn  time.Time
	WantedOn time.Time
}

// Decision is the answer, and is the whole answer: a refusal always carries the
// reason, so no caller has to infer why from a bare false.
type Decision struct {
	Booked bool
	Slot   SlotKind
	Reason Reason
}

// Scheduler holds the districts this division inspects and the appointments
// already committed.
type Scheduler struct {
	calendar *Calendar
	capacity map[string]Capacity
	routine  map[string]int
	reserve  map[string]int
}

// NewScheduler returns a scheduler that inspects no districts yet; call
// SetCapacity for each district it is responsible for.
func NewScheduler(calendar *Calendar) *Scheduler {
	return &Scheduler{
		calendar: calendar,
		capacity: make(map[string]Capacity),
		routine:  make(map[string]int),
		reserve:  make(map[string]int),
	}
}

// SetCapacity declares a district and the appointments it can staff in a day.
func (s *Scheduler) SetCapacity(district string, capacity Capacity) {
	s.capacity[district] = capacity
}

// Booked reports how many inspections of either kind a district has committed on
// a day.
func (s *Scheduler) Booked(district string, day time.Time) int {
	key := slotKey(district, day)
	return s.routine[key] + s.reserve[key]
}

// Book applies the rules in a fixed order and, if the request survives all of
// them, commits an appointment.
//
// The order is itself a rule. A request can break several at once -- a
// suspended permit asking for a holiday with no notice on a full day -- and the
// contractor gets exactly one reason. Cheapest and most fundamental first:
// jurisdiction, then the permit's standing, then the shape of the request, then
// the calendar, then notice, and only then capacity. Capacity is last because it
// is the only rule whose answer changes minute to minute; telling somebody their
// day is full when their permit was never valid would send them off to solve the
// wrong problem.
func (s *Scheduler) Book(request Request) Decision {
	capacity, served := s.capacity[request.District]
	if !served {
		return refused(ReasonUnknownDistrict)
	}
	if request.Permit != PermitActive {
		return refused(ReasonPermitNotActive)
	}
	required, published := noticeRequired[request.Priority]
	if !published {
		return refused(ReasonUnknownPriority)
	}
	if startOfDay(request.WantedOn).Before(startOfDay(request.FiledOn)) {
		return refused(ReasonDateInPast)
	}
	if !s.calendar.IsBusinessDay(request.WantedOn) {
		return refused(ReasonNotABusinessDay)
	}
	if s.calendar.NoticeDays(request.FiledOn, request.WantedOn) < required {
		return refused(ReasonInsufficientNotice)
	}

	key := slotKey(request.District, request.WantedOn)
	if s.routine[key] < capacity.Routine {
		s.routine[key]++
		return Decision{Booked: true, Slot: SlotRoutine}
	}
	// The reserve is not a general overflow pool. Only an emergency may take it,
	// and only while it lasts.
	if request.Priority == PriorityEmergency && s.reserve[key] < capacity.Reserve {
		s.reserve[key]++
		return Decision{Booked: true, Slot: SlotReserve}
	}
	return refused(ReasonNoCapacity)
}

// refused builds the one shape a refusal may take.
func refused(reason Reason) Decision {
	return Decision{Booked: false, Slot: SlotNone, Reason: reason}
}

// slotKey identifies one district's one day.
func slotKey(district string, day time.Time) string {
	return district + "|" + dayKey(day)
}
