package ingest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

func TestTruenasRelay(t *testing.T) {
	var gotTitle, gotMsg string
	calls := 0
	h := &Handler{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Token: "tok",
		Relay: func(_ context.Context, title, msg string) error {
			calls++
			gotTitle, gotMsg = title, msg
			return nil
		},
	}
	r := chi.NewRouter()
	h.Routes(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	post := func(path, body string) int {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("/webhook/truenas", `{"text":"x"}`); code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", code)
	}
	if code := post("/webhook/truenas?token=tok", `{"text":"TrueNAS @ nas\n\nNew alert:\n* Cloud Backup Task failed"}`); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if calls != 1 || gotTitle != "TrueNAS" || !strings.Contains(gotMsg, "Cloud Backup Task failed") {
		t.Fatalf("relay calls=%d title=%q msg=%q", calls, gotTitle, gotMsg)
	}
	post("/webhook/truenas?token=tok", `{"text":"`+strings.Repeat("a", 2000)+`"}`)
	if calls != 2 || utf8.RuneCountInString(gotMsg) > pushoverMaxMessage {
		t.Fatalf("long text: calls=%d runes=%d", calls, utf8.RuneCountInString(gotMsg))
	}
	post("/webhook/truenas?token=tok", `{"text":"  "}`)
	if calls != 2 {
		t.Fatalf("empty text was relayed")
	}

	// Without a relay the route does not exist.
	r2 := chi.NewRouter()
	(&Handler{Log: h.Log, Token: "tok"}).Routes(r2)
	srv2 := httptest.NewServer(r2)
	defer srv2.Close()
	resp, err := http.Post(srv2.URL+"/webhook/truenas?token=tok", "application/json", strings.NewReader(`{"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("route served without relay")
	}
}
