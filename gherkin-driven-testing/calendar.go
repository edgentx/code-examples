package inspection

import "time"

// Calendar knows which days the inspection division actually works. Weekends and
// observed holidays are not business days, which has two consequences the
// acceptance criteria care about: an appointment cannot land on one, and one
// never counts toward the notice a contractor has given.
type Calendar struct {
	holidays map[string]string
}

// NewCalendar returns a calendar that observes weekends only.
func NewCalendar() *Calendar {
	return &Calendar{holidays: make(map[string]string)}
}

// Observe records a holiday on the day it is observed, which is not always the
// day of the holiday itself: a holiday falling on a Saturday is commonly
// observed on the preceding Friday, and it is the observed day the office is
// closed on.
func (c *Calendar) Observe(day time.Time, name string) {
	c.holidays[dayKey(day)] = name
}

// IsBusinessDay reports whether inspectors are working that day.
func (c *Calendar) IsBusinessDay(day time.Time) bool {
	switch day.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	_, observed := c.holidays[dayKey(day)]
	return !observed
}

// NoticeDays counts the business days a contractor has given, which is every
// business day after the day the request was filed up to and including the day
// wanted. The filing day never counts, because a request filed at 4:55 p.m. is
// not a day of warning; the appointment day does count, because that is the day
// the crew has to be routed.
//
// Filed on a Monday for the Thursday is three business days of notice. Filed on
// a Thursday for the following Monday is two: the weekend is not notice, and
// neither is an observed holiday in between.
func (c *Calendar) NoticeDays(filedOn, wantedOn time.Time) int {
	filed, wanted := startOfDay(filedOn), startOfDay(wantedOn)
	days := 0
	for day := filed.AddDate(0, 0, 1); !day.After(wanted); day = day.AddDate(0, 0, 1) {
		if c.IsBusinessDay(day) {
			days++
		}
	}
	return days
}

// startOfDay drops the clock time. Everything in this domain is decided at the
// granularity of a day, so a request filed at 08:00 and one filed at 16:59 on
// the same date must be treated identically.
func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

// dayKey is the calendar-date identity of a moment, used to key holidays and
// bookings.
func dayKey(t time.Time) string {
	return t.Format("2006-01-02")
}
