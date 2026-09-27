// Package alert tells a person when a service stays unhealthy. The health
// poller only recorded status for the dashboard, so an outage was noticed
// when someone happened to look (arrarr's frozen poller went 6 days).
//
// One message when a service has been down (or degraded) long enough, one
// when it recovers — never a message per poll.
package alert

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sender delivers one alert.
type Sender interface {
	Send(ctx context.Context, title, message string) error
}

// Pushover sends through the Pushover API. Deliberately not WhatsApp: the
// alert must still arrive when notifyarr or WAHA is what broke.
type Pushover struct {
	Token, User string
	HTTP        *http.Client
	BaseURL     string // tests; default https://api.pushover.net
}

func (p *Pushover) Send(ctx context.Context, title, message string) error {
	base := p.BaseURL
	if base == "" {
		base = "https://api.pushover.net"
	}
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{"token": {p.Token}, "user": {p.User}, "title": {title}, "message": {message}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/1/messages.json", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pushover: status %d", resp.StatusCode)
	}
	return nil
}

// Alerter tracks each service's status across health passes.
type Alerter struct {
	Sender Sender
	Log    *slog.Logger
	// DownAfter / DegradedAfter: how long a service must stay in that state
	// before an alert goes out. Degraded waits longer — an indexer hiccup or a
	// pending WhatsApp re-pair is not worth a ping within minutes.
	DownAfter     time.Duration
	DegradedAfter time.Duration
	// Skip lists services never alerted on (Journarr's own tile).
	Skip map[string]bool
	Now  func() time.Time

	mu    sync.Mutex
	state map[string]*svcState
}

type svcState struct {
	since   time.Time // first non-up observation of this incident
	alerted string    // status we alerted on ("" = none yet)
}

var severity = map[string]int{"up": 0, "degraded": 1, "down": 2}

// Observe feeds one health result. Safe for concurrent use.
func (a *Alerter) Observe(ctx context.Context, service, status, detail string) {
	if a == nil || a.Sender == nil || a.Skip[service] {
		return
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	var title, msg string
	a.mu.Lock()
	if a.state == nil {
		a.state = map[string]*svcState{}
	}
	st := a.state[service]
	switch status {
	case "up":
		if st != nil && st.alerted != "" {
			title = service + " recovered"
			msg = fmt.Sprintf("%s is up again after %s.", service, now.Sub(st.since).Round(time.Minute))
		}
		delete(a.state, service)
	default:
		if st == nil {
			st = &svcState{since: now}
			a.state[service] = st
		}
		wait := a.DegradedAfter
		if status == "down" {
			wait = a.DownAfter
		}
		// Alert once per incident, again only if it gets worse.
		if now.Sub(st.since) >= wait && severity[status] > severity[st.alerted] {
			st.alerted = status
			title = fmt.Sprintf("%s %s", service, status)
			msg = fmt.Sprintf("%s has been %s for %s.", service, status, now.Sub(st.since).Round(time.Minute))
			if detail != "" {
				msg += "\n" + truncate(detail, 400)
			}
		}
	}
	a.mu.Unlock()

	if title == "" {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := a.Sender.Send(sctx, "Journarr: "+title, msg); err != nil {
		a.Log.Error("alert: send failed", "service", service, "err", err)
		return
	}
	a.Log.Info("alert: sent", "service", service, "title", title)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
