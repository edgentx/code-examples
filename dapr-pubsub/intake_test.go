package intake

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNoticeValidation(t *testing.T) {
	valid := Notice{NoticeID: "N-1001", AgencyCode: "DPR", SeriesCode: "RS-100", PageCount: 12}

	cases := []struct {
		name    string
		mutate  func(*Notice)
		wantErr error
	}{
		{name: "complete notice", mutate: func(*Notice) {}},
		{name: "no notice id", mutate: func(n *Notice) { n.NoticeID = "" }, wantErr: ErrMissingField},
		{name: "no agency code", mutate: func(n *Notice) { n.AgencyCode = "" }, wantErr: ErrMissingField},
		{name: "no series code", mutate: func(n *Notice) { n.SeriesCode = "" }, wantErr: ErrMissingField},
		{name: "zero pages", mutate: func(n *Notice) { n.PageCount = 0 }, wantErr: ErrNotPositive},
		{name: "negative pages", mutate: func(n *Notice) { n.PageCount = -3 }, wantErr: ErrNotPositive},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			notice := valid
			testCase.mutate(&notice)
			if err := notice.Validate(); !errors.Is(err, testCase.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func testAPI(publisher Publisher) IntakeAPI {
	return IntakeAPI{
		Publisher: publisher,
		Topic:     "intake-notices",
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return time.Date(2026, 3, 4, 8, 30, 0, 0, time.UTC) },
	}
}

// TestIntakeEndpoint exercises the publishing service end to end over HTTP with
// no sidecar running, which is the point of the publisher port.
func TestIntakeEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		publishErr   error
		wantStatus   int
		wantPublishe bool
	}{
		{
			name:         "valid notice is accepted and published",
			body:         `{"noticeId":"N-1001","agencyCode":"DPR","seriesCode":"RS-100","pageCount":12}`,
			wantStatus:   http.StatusAccepted,
			wantPublishe: true,
		},
		{
			name:       "malformed body is rejected before the topic sees it",
			body:       `{"noticeId":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid notice is rejected before the topic sees it",
			body:       `{"noticeId":"N-1002","agencyCode":"DPR","seriesCode":"","pageCount":12}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a failed publish is reported, not swallowed",
			body:       `{"noticeId":"N-1003","agencyCode":"DPR","seriesCode":"RS-100","pageCount":12}`,
			publishErr: ErrPublishRejected,
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			publisher := &fakePublisher{err: testCase.publishErr}
			server := httptest.NewServer(testAPI(publisher).Routes())
			defer server.Close()

			response, err := http.Post(server.URL+"/intake", "application/json", strings.NewReader(testCase.body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != testCase.wantStatus {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d (body %s)", response.StatusCode, testCase.wantStatus, body)
			}
			if published := len(publisher.events) == 1; published != testCase.wantPublishe {
				t.Errorf("published = %v, want %v", published, testCase.wantPublishe)
			}
		})
	}
}

// TestIntakeEventIdIsStable is the idempotency guarantee stated as a test.
// Publishing the same notice twice produces the same event id, which is the
// only thing that lets a consumer recognize the repeat.
func TestIntakeEventIdIsStable(t *testing.T) {
	publisher := &fakePublisher{}
	handler := testAPI(publisher).Routes()
	body := `{"noticeId":"N-1001","agencyCode":"DPR","seriesCode":"RS-100","pageCount":12}`

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/intake", strings.NewReader(body))
		request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}

	if len(publisher.events) != 2 {
		t.Fatalf("published %d events, want 2", len(publisher.events))
	}
	first, second := publisher.events[0].event, publisher.events[1].event
	if first.ID != "N-1001" || second.ID != "N-1001" {
		t.Errorf("event ids = %q and %q, want both derived from the notice id", first.ID, second.ID)
	}
	if first.Type != TypeIntakeNotice {
		t.Errorf("type = %q, want %q", first.Type, TypeIntakeNotice)
	}
	if first.Time != "2026-03-04T08:30:00Z" {
		t.Errorf("time = %q, want the injected clock", first.Time)
	}
	if first.TraceParent == "" {
		t.Error("traceparent from the inbound request should be carried into the envelope")
	}

	var carried Notice
	if err := json.Unmarshal(first.Data, &carried); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if carried.PageCount != 12 {
		t.Errorf("data = %+v, want the notice intact", carried)
	}
}
