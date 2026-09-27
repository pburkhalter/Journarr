package alert

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) Send(_ context.Context, title, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, title)
	return nil
}

func TestAlertsOncePerIncidentAndOnRecovery(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	rec := &recorder{}
	a := &Alerter{Sender: rec, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DownAfter: 10 * time.Minute, DegradedAfter: 2 * time.Hour,
		Skip: map[string]bool{"journarr": true}, Now: func() time.Time { return now }}
	ctx := context.Background()
	step := func(d time.Duration, svc, status string) {
		now = now.Add(d)
		a.Observe(ctx, svc, status, "detail")
	}

	step(0, "sonarr", "down")
	step(5*time.Minute, "sonarr", "down") // not yet
	step(6*time.Minute, "sonarr", "down") // 11 min → alert
	step(1*time.Minute, "sonarr", "down") // no repeat
	step(1*time.Minute, "sonarr", "up")   // recovered
	step(0, "radarr", "degraded")
	step(90*time.Minute, "radarr", "degraded") // below 2h
	step(0, "journarr", "down")
	step(time.Hour, "journarr", "down")        // skipped
	step(31*time.Minute, "radarr", "degraded") // 2h01 → alert
	step(time.Minute, "radarr", "down")        // worse → alert again
	step(time.Minute, "radarr", "up")

	want := []string{"sonarr down", "sonarr recovered", "radarr degraded", "radarr down", "radarr recovered"}
	for i := range want {
		want[i] = "Journarr: " + want[i]
	}
	if strings.Join(rec.sent, "|") != strings.Join(want, "|") {
		t.Fatalf("sent %q\nwant %q", rec.sent, want)
	}
}

// A blip shorter than the threshold never pages anyone.
func TestShortOutageStaysQuiet(t *testing.T) {
	now := time.Now()
	rec := &recorder{}
	a := &Alerter{Sender: rec, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DownAfter: 10 * time.Minute, DegradedAfter: time.Hour, Now: func() time.Time { return now }}
	a.Observe(context.Background(), "arrarr", "down", "")
	now = now.Add(5 * time.Minute)
	a.Observe(context.Background(), "arrarr", "up", "")
	if len(rec.sent) != 0 {
		t.Fatalf("sent %v for a 5-minute blip", rec.sent)
	}
}

func TestPushoverForm(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{"path": r.URL.Path, "token": r.Form.Get("token"), "user": r.Form.Get("user"), "title": r.Form.Get("title")}
		_, _ = io.WriteString(w, `{"status":1}`)
	}))
	defer srv.Close()
	p := &Pushover{Token: "t", User: "u", BaseURL: srv.URL}
	if err := p.Send(context.Background(), "Journarr: x down", "m"); err != nil {
		t.Fatal(err)
	}
	if got["path"] != "/1/messages.json" || got["token"] != "t" || got["user"] != "u" || got["title"] != "Journarr: x down" {
		t.Fatalf("form = %v", got)
	}
}
