package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Scripts could not call the API at all: it accepted only an SSO session.
func TestAPITokenAuthenticatesBearerHeaderOnly(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	a := New(Config{IssuerURL: "https://id.example", SessionSecret: "0123456789abcdef",
		SessionMaxAge: time.Hour, APIToken: token}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := a.RequireAPI(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	for _, c := range []struct {
		name, auth, query string
		want              int
	}{
		{"bearer", "Bearer " + token, "", http.StatusOK},
		{"wrong token", "Bearer nope", "", http.StatusUnauthorized},
		{"no header", "", "", http.StatusUnauthorized},
		{"token in query is not accepted", "", "?token=" + token, http.StatusUnauthorized},
		{"basic scheme", "Basic " + token, "", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest("GET", "/api/requests"+c.query, nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}

	// Without a configured token, a bearer header grants nothing.
	b := New(Config{IssuerURL: "https://id.example", SessionSecret: "0123456789abcdef", SessionMaxAge: time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest("GET", "/api/requests", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	b.RequireAPI(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty token config accepted a bearer header: %d", w.Code)
	}
}
