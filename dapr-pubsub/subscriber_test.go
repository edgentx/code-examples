package intake

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testSubscriber(store Store, publisher Publisher) Subscriber {
	return Subscriber{
		Store:           store,
		Publisher:       publisher,
		DeadLetterTopic: "intake-notices-parked",
		MaxAttempts:     3,
		Catalog:         map[string]bool{"RS-100": true, "RS-220": true},
		Log:             slog.New(slog.DiscardHandler),
		Now:             func() time.Time { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) },
	}
}

func envelope(id, eventType, seriesCode string) string {
	notice := Notice{NoticeID: id, AgencyCode: "DPR", SeriesCode: seriesCode, PageCount: 12}
	data, err := json.Marshal(notice)
	if err != nil {
		panic(err)
	}
	event := NewCloudEvent(eventType, SourcePublisher, id, time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC), data)
	body, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	return string(body)
}

// deliver drives one delivery through the real HTTP surface and returns the
// status code and the exact body, because the body is the contract.
func deliver(t *testing.T, handler http.HandlerFunc, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/events/intake", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response.StatusCode, string(raw)
}

// TestDeliveryContract is the point of the example: every verdict is a 200, and
// what distinguishes them is the body. Asserting the exact byte string is
// deliberate -- a stray newline or a "SUCCESS" spelled differently changes the
// message's fate, and no compiler will tell you.
func TestDeliveryContract(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		setup      func(*fakeStore)
		wantStatus int
		wantBody   string
	}{
		{
			name:       "processable notice is acknowledged with an empty body",
			body:       envelope("N-1001", TypeIntakeNotice, "RS-100"),
			wantStatus: http.StatusOK,
			wantBody:   "",
		},
		{
			name:       "unprocessable notice asks for redelivery",
			body:       envelope("N-1002", TypeIntakeNotice, "RS-999"),
			wantStatus: http.StatusOK,
			wantBody:   RetryBody,
		},
		{
			name:       "event this consumer does not own is dropped",
			body:       envelope("N-1003", "gov.example.permits.issued.v1", "RS-100"),
			wantStatus: http.StatusOK,
			wantBody:   DropBody,
		},
		{
			name:       "unparseable body is dropped",
			body:       "{not json",
			wantStatus: http.StatusOK,
			wantBody:   DropBody,
		},
		{
			name:       "envelope missing an id is dropped",
			body:       `{"specversion":"1.0","type":"` + TypeIntakeNotice + `","source":"/records/intake-api"}`,
			wantStatus: http.StatusOK,
			wantBody:   DropBody,
		},
		{
			name: "state store outage asks for redelivery rather than acknowledging",
			body: envelope("N-1004", TypeIntakeNotice, "RS-100"),
			setup: func(store *fakeStore) {
				store.failGet = errStoreDown
			},
			wantStatus: http.StatusOK,
			wantBody:   RetryBody,
		},
		{
			name: "lost etag race asks for redelivery rather than double-counting",
			body: envelope("N-1005", TypeIntakeNotice, "RS-100"),
			setup: func(store *fakeStore) {
				store.failSaveOnce = fmt.Errorf("%w: attempt::N-1005", ErrETagConflict)
			},
			wantStatus: http.StatusOK,
			wantBody:   RetryBody,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newFakeStore()
			if testCase.setup != nil {
				testCase.setup(store)
			}
			subscriber := testSubscriber(store, &fakePublisher{})

			status, body := deliver(t, subscriber.HandleDelivery, testCase.body)
			if status != testCase.wantStatus {
				t.Errorf("status = %d, want %d", status, testCase.wantStatus)
			}
			if body != testCase.wantBody {
				t.Errorf("body = %q, want %q", body, testCase.wantBody)
			}
		})
	}
}

// TestPoisonMessageIsParkedNotDropped walks the whole path the agency ask cares
// about: a message that can never be processed is retried up to the budget,
// then parked on the dead-letter topic, and the dead-letter route turns it into
// a record a human can look up. At no point is it acknowledged unprocessed.
func TestPoisonMessageIsParkedNotDropped(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	subscriber := testSubscriber(store, publisher)
	body := envelope("N-2001", TypeIntakeNotice, "RS-999")

	wantBodies := []string{RetryBody, RetryBody, ""}
	for attempt, want := range wantBodies {
		status, got := deliver(t, subscriber.HandleDelivery, body)
		if status != http.StatusOK {
			t.Fatalf("delivery %d: status = %d, want 200", attempt+1, status)
		}
		if got != want {
			t.Fatalf("delivery %d: body = %q, want %q", attempt+1, got, want)
		}
	}

	// The budget was spent on the third delivery, so the message left by the
	// dead-letter topic rather than by a silent acknowledgement.
	if len(publisher.events) != 1 {
		t.Fatalf("published %d events, want exactly one park", len(publisher.events))
	}
	parked := publisher.last()
	if parked.topic != "intake-notices-parked" {
		t.Errorf("parked to %q, want the dead-letter topic", parked.topic)
	}
	if parked.event.ID != "N-2001" {
		t.Errorf("parked event id = %q, want the original id preserved", parked.event.ID)
	}
	if !strings.Contains(parked.event.ParkReason, "retention catalog") {
		t.Errorf("park reason = %q, want the processing failure", parked.event.ParkReason)
	}

	// Now the dead-letter route, which is where a human can see it.
	raw, err := json.Marshal(parked.event)
	if err != nil {
		t.Fatalf("encode parked event: %v", err)
	}
	status, deadLetterBody := deliver(t, subscriber.HandleDeadLetter, string(raw))
	if status != http.StatusOK || deadLetterBody != "" {
		t.Fatalf("dead-letter route answered %d %q, want an acknowledgement", status, deadLetterBody)
	}

	stored, _, err := store.Get(t.Context(), "parked::N-2001")
	if err != nil || stored == nil {
		t.Fatalf("no parked record written: %v", err)
	}
	var record ParkedNotice
	if err := json.Unmarshal(stored, &record); err != nil {
		t.Fatalf("decode parked record: %v", err)
	}
	if record.Attempts != 3 {
		t.Errorf("recorded attempts = %d, want the spent budget of 3", record.Attempts)
	}
	if record.ParkedAt != "2026-03-04T09:00:00Z" {
		t.Errorf("parkedAt = %q, want the injected clock", record.ParkedAt)
	}
}

// TestParkFailureRetriesRatherThanAcknowledges is the ugly case. If the message
// cannot be parked, acknowledging it would lose it. The handler must hand it
// back instead, even though the delivery budget is already spent.
func TestParkFailureRetriesRatherThanAcknowledges(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{err: ErrPublishRejected}
	subscriber := testSubscriber(store, publisher)
	body := envelope("N-3001", TypeIntakeNotice, "RS-999")

	var lastBody string
	for range 4 {
		_, lastBody = deliver(t, subscriber.HandleDelivery, body)
	}
	if lastBody != RetryBody {
		t.Errorf("body = %q, want %q: an unparkable message must never be acknowledged", lastBody, RetryBody)
	}
}

// TestDuplicateDeliveryIsAcknowledgedOnce proves what the stable event id buys.
// The second sighting of the same id is recognized and acknowledged without
// being counted as a new delivery attempt.
func TestDuplicateDeliveryIsAcknowledgedOnce(t *testing.T) {
	store := newFakeStore()
	subscriber := testSubscriber(store, &fakePublisher{})
	body := envelope("N-4001", TypeIntakeNotice, "RS-220")

	if _, got := deliver(t, subscriber.HandleDelivery, body); got != "" {
		t.Fatalf("first delivery body = %q, want an acknowledgement", got)
	}
	if _, got := deliver(t, subscriber.HandleDelivery, body); got != "" {
		t.Fatalf("second delivery body = %q, want an acknowledgement", got)
	}

	counter, _, err := store.Get(t.Context(), "attempt::N-4001")
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if decodeAttempts(counter) != 1 {
		t.Errorf("attempts = %d, want 1: a duplicate is not a new attempt", decodeAttempts(counter))
	}
}

// TestParkedLookup covers the operator's view of the queue.
func TestParkedLookup(t *testing.T) {
	store := newFakeStore()
	subscriber := testSubscriber(store, &fakePublisher{})
	server := httptest.NewServer(subscriber.Routes())
	defer server.Close()

	response, err := http.Get(server.URL + "/parked/N-9999")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an event that was never parked", response.StatusCode)
	}
}
