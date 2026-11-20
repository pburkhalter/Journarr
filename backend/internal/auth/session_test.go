package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sessionCookie logs u in through the real cookie store and returns the
// cookie a browser would send back.
func sessionCookie(t *testing.T, a *Auth, u *User) *http.Cookie {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, _ := a.store.New(req, sessionName)
	sess.Values[userKey] = u
	if err := sess.Save(req, rec); err != nil {
		t.Fatal(err)
	}
	return rec.Result().Cookies()[0]
}

// Approving a removal an LLM asked for must take a person: an SSO session,
// never a token — not even the API token, which RequireAPI does accept.
func TestRequireSessionAdmitsOnlyAnSSOSession(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(Config{IssuerURL: "https://id.example", SessionSecret: "0123456789abcdef",
		SessionMaxAge: time.Hour, APIToken: token}, log)
	h := a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	cookie := sessionCookie(t, a, &User{Subject: "u1", Email: "owner@example.com"})

	for _, c := range []struct {
		name   string
		auth   string
		cookie bool
		want   int
	}{
		{"session", "", true, http.StatusOK},
		{"api token", "Bearer " + token, false, http.StatusForbidden},
		{"api token plus session", "Bearer " + token, true, http.StatusForbidden},
		{"other bearer", "Bearer mcp-token", false, http.StatusForbidden},
		{"nothing", "", false, http.StatusUnauthorized},
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/approvals/1/approve", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		if c.cookie {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
	}
	if got := a.Actor(func() *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		r.AddCookie(cookie)
		return r
	}()); got != "owner@example.com" {
		t.Errorf("Actor = %q", got)
	}

	// Open LAN mode has no owner to ask: nobody may approve.
	open := New(Config{}, log)
	rec := httptest.NewRecorder()
	open.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("open mode: status %d, want 403", rec.Code)
	}
}
