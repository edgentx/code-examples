package inspection_test

import (
	"testing"
	"time"

	inspection "github.com/edgentx/code-examples/gherkin-driven-testing"
)

// The feature files pin the rules the division agreed to. These tests pin the
// arithmetic underneath them, at a granularity nobody would want to read in
// Gherkin: every weekday, both ends of an interval, and the clock time within a
// day.

// holidayCalendar observes the Friday before the 2026 Independence Day weekend,
// which is the observed date for a holiday that falls on a Saturday.
func holidayCalendar(t *testing.T) *inspection.Calendar {
	t.Helper()
	calendar := inspection.NewCalendar()
	calendar.Observe(mustDay(t, "2026-07-03"), "Independence Day")
	return calendar
}

func TestIsBusinessDay(t *testing.T) {
	calendar := holidayCalendar(t)

	cases := []struct {
		name     string
		day      string
		business bool
	}{
		{name: "ordinary Monday", day: "2026-06-15", business: true},
		{name: "ordinary Friday", day: "2026-06-19", business: true},
		{name: "Saturday", day: "2026-06-20", business: false},
		{name: "Sunday", day: "2026-06-21", business: false},
		{name: "observed holiday", day: "2026-07-03", business: false},
		{name: "the weekday after an observed holiday", day: "2026-07-06", business: true},
		{name: "the same date one year on, when nothing is observed", day: "2027-07-03", business: false}, // a Saturday
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := calendar.IsBusinessDay(mustDay(t, testCase.day))
			if got != testCase.business {
				t.Fatalf("IsBusinessDay(%s) = %t, want %t", testCase.day, got, testCase.business)
			}
		})
	}
}

func TestNoticeDays(t *testing.T) {
	calendar := holidayCalendar(t)

	cases := []struct {
		name   string
		filed  string
		wanted string
		days   int
	}{
		{name: "the filing day is never notice", filed: "2026-06-15", wanted: "2026-06-15", days: 0},
		{name: "the next weekday is one day", filed: "2026-06-15", wanted: "2026-06-16", days: 1},
		{name: "Monday to Thursday is three days", filed: "2026-06-15", wanted: "2026-06-18", days: 3},
		{name: "the weekend does not count", filed: "2026-06-18", wanted: "2026-06-22", days: 2},
		{name: "Thursday to the following Tuesday is three days", filed: "2026-06-18", wanted: "2026-06-23", days: 3},
		{name: "an observed holiday does not count", filed: "2026-06-30", wanted: "2026-07-06", days: 3},
		{name: "a full week is five days", filed: "2026-06-15", wanted: "2026-06-22", days: 5},
		{name: "a day already past gives no notice", filed: "2026-06-18", wanted: "2026-06-17", days: 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := calendar.NoticeDays(mustDay(t, testCase.filed), mustDay(t, testCase.wanted))
			if got != testCase.days {
				t.Fatalf("NoticeDays(%s, %s) = %d, want %d",
					testCase.filed, testCase.wanted, got, testCase.days)
			}
		})
	}
}

// TestNoticeIgnoresClockTime is the reason the domain truncates to the start of
// the day. A request filed at 08:00 and one filed at 16:59 have given the same
// notice, and a scheduler that disagreed would refuse the second one for a rule
// nobody published.
func TestNoticeIgnoresClockTime(t *testing.T) {
	calendar := inspection.NewCalendar()
	wanted := mustDay(t, "2026-06-18")

	morning := mustDay(t, "2026-06-15").Add(8 * time.Hour)
	endOfDay := mustDay(t, "2026-06-15").Add(16*time.Hour + 59*time.Minute)

	if got, want := calendar.NoticeDays(morning, wanted), 3; got != want {
		t.Fatalf("notice from the morning filing = %d, want %d", got, want)
	}
	if got, want := calendar.NoticeDays(endOfDay, wanted), 3; got != want {
		t.Fatalf("notice from the end-of-day filing = %d, want %d", got, want)
	}
}

// mustDay parses a calendar date or fails the test, so no test body carries an
// error check for a constant it wrote itself.
func mustDay(t *testing.T, date string) time.Time {
	t.Helper()
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("test date %q is unparseable: %v", date, err)
	}
	return day
}
