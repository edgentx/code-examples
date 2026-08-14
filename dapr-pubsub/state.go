package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Store is the second building block: a key/value store reached through the
// sidecar, with optimistic concurrency. Two methods, because that is all this
// example needs and a narrow port is what makes the subscriber testable.
type Store interface {
	// Get returns the stored value and its etag. A missing key is not an
	// error: it returns (nil, "", nil), because "no counter yet" is the normal
	// state of a first delivery.
	Get(ctx context.Context, key string) (json.RawMessage, string, error)
	// Save writes conditionally. An empty etag means "create; fail if this key
	// already exists". A non-empty etag means "update; fail unless the stored
	// value is still the one I read". Either failure is ErrETagConflict.
	Save(ctx context.Context, key string, value json.RawMessage, etag string) error
}

// SidecarStore talks to the daprd state API. As with the publisher, this is the
// only place that knows the URL shape.
type SidecarStore struct {
	BaseURL   string
	Component string
	Client    *http.Client
}

func (s SidecarStore) Get(ctx context.Context, key string) (json.RawMessage, string, error) {
	url := fmt.Sprintf("%s/v1.0/state/%s/%s", s.BaseURL, s.Component, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: build get request: %v", ErrStateFailed, err)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: get %s: %v", ErrStateFailed, key, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var body bytes.Buffer
		if _, err := body.ReadFrom(resp.Body); err != nil {
			return nil, "", fmt.Errorf("%w: read %s: %v", ErrStateFailed, key, err)
		}
		// The etag comes back in a header, not the body. Holding on to it is
		// what makes the next write conditional.
		return json.RawMessage(body.Bytes()), resp.Header.Get("ETag"), nil
	case http.StatusNoContent, http.StatusNotFound:
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("%w: get %s returned %d", ErrStateFailed, key, resp.StatusCode)
	}
}

// stateItem is the sidecar's save request shape. The API takes an array, so a
// caller can write several keys at once; this example writes one at a time.
type stateItem struct {
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
	ETag    string          `json:"etag,omitempty"`
	Options struct {
		Concurrency string `json:"concurrency"`
		Consistency string `json:"consistency"`
	} `json:"options"`
}

func (s SidecarStore) Save(ctx context.Context, key string, value json.RawMessage, etag string) error {
	item := stateItem{Key: key, Value: value, ETag: etag}
	// first-write concurrency is what turns the etag into a guard. With
	// last-write (the default) the etag is accepted and ignored, and a lost
	// update looks exactly like a successful one.
	item.Options.Concurrency = "first-write"
	item.Options.Consistency = "strong"

	body, err := json.Marshal([]stateItem{item})
	if err != nil {
		return fmt.Errorf("%w: encode %s: %v", ErrStateFailed, key, err)
	}
	url := fmt.Sprintf("%s/v1.0/state/%s", s.BaseURL, s.Component)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build save request: %v", ErrStateFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("%w: save %s: %v", ErrStateFailed, key, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusConflict:
		// Somebody else wrote this key between our read and our write. This is
		// an expected outcome under concurrent delivery, not an outage.
		return fmt.Errorf("%w: save %s", ErrETagConflict, key)
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil
	default:
		return fmt.Errorf("%w: save %s returned %d", ErrStateFailed, key, resp.StatusCode)
	}
}

func (s SidecarStore) client() *http.Client {
	if s.Client == nil {
		return http.DefaultClient
	}
	return s.Client
}
