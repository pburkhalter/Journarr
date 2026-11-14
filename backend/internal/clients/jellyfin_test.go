package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// jellyfinRequest is what the tests need to know about one request.
type jellyfinRequest struct {
	header http.Header
	query  url.Values
}

// jellyfinRecorder is a stand-in Jellyfin that answers every path with a
// minimal valid body and remembers the requests it received.
type jellyfinRecorder struct {
	*httptest.Server
	mu       sync.Mutex
	requests []jellyfinRequest
}

func newJellyfinRecorder(t *testing.T) *jellyfinRecorder {
	t.Helper()
	rec := &jellyfinRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.requests = append(rec.requests, jellyfinRequest{header: r.Header.Clone(), query: r.URL.Query()})
		rec.mu.Unlock()

		switch r.URL.Path {
		case "/Sessions":
			_, _ = w.Write([]byte(`[]`))
		case "/Library/Refresh":
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"Items":[{"ProviderIds":{"Tvdb":"1"}}]}`))
		}
	}))
	t.Cleanup(rec.Close)
	return rec
}

// only returns the single request a call is expected to make.
func (rec *jellyfinRecorder) only(t *testing.T) jellyfinRequest {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(rec.requests))
	}
	return rec.requests[0]
}

// jellyfinCalls lists every method that talks to the server. A method added
// later belongs here, so that it cannot ship without credentials.
var jellyfinCalls = []struct {
	name string
	call func(context.Context, *Jellyfin) error
}{
	{"CheckHealth", func(ctx context.Context, c *Jellyfin) error {
		if res := c.CheckHealth(ctx); res.Status != "up" {
			return errors.New("status " + res.Status)
		}
		return nil
	}},
	{"RecentlyAdded", func(ctx context.Context, c *Jellyfin) error {
		_, err := c.RecentlyAdded(ctx, 10)
		return err
	}},
	{"ItemsByDateCreated", func(ctx context.Context, c *Jellyfin) error {
		_, err := c.ItemsByDateCreated(ctx, 0, 10)
		return err
	}},
	{"SeriesTvdbID", func(ctx context.Context, c *Jellyfin) error {
		_, err := c.SeriesTvdbID(ctx, "series-1")
		return err
	}},
	{"ItemsByName", func(ctx context.Context, c *Jellyfin) error {
		_, err := c.ItemsByName(ctx, "Silo")
		return err
	}},
	{"RefreshLibrary", func(ctx context.Context, c *Jellyfin) error {
		return c.RefreshLibrary(ctx)
	}},
	{"Sessions", func(ctx context.Context, c *Jellyfin) error {
		_, err := c.Sessions(ctx)
		return err
	}},
}

func TestJellyfin_SendsKeyInAuthorizationHeader(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	for _, tc := range jellyfinCalls {
		t.Run(tc.name, func(t *testing.T) {
			rec := newJellyfinRecorder(t)
			c := NewJellyfin(rec.URL, key, 5*time.Second)

			if err := tc.call(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			req := rec.only(t)

			if got, want := req.header.Get("Authorization"), `MediaBrowser Token="`+key+`"`; got != want {
				t.Errorf("Authorization = %q, want %q", got, want)
			}
			// Jellyfin 12 rejects these legacy forms; they must not return.
			if got := req.header.Get("X-Emby-Token"); got != "" {
				t.Errorf("X-Emby-Token = %q, want it unset", got)
			}
			for _, param := range []string{"api_key", "ApiKey"} {
				if req.query.Has(param) {
					t.Errorf("query parameter %s is set, want the key in the header only", param)
				}
			}
		})
	}
}

func TestJellyfin_WithoutKeySendsNoCredentials(t *testing.T) {
	for _, tc := range jellyfinCalls {
		t.Run(tc.name, func(t *testing.T) {
			rec := newJellyfinRecorder(t)
			c := NewJellyfin(rec.URL, "", 5*time.Second)

			if err := tc.call(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			if got := rec.only(t).header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want it unset without a key", got)
			}
		})
	}
}
