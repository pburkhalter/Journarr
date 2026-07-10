// Package updates checks GitHub for the self-hosted custom stack (arrarr,
// waha-concierge, journarr) and reports whether a newer tagged version than the
// running one exists — the equivalent of the *arr apps' built-in "new update
// available" check, which we can't get for our own images. It reads the repo's
// git tags (these repos tag releases but don't publish GitHub Releases). Results
// are cached in memory (GitHub's unauthenticated rate limit is 60/h) and merged
// into /api/services.
package updates

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pburkhalter/journarr/internal/store"
)

// Info is the per-service update status shown on its card.
type Info struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
}

type Checker struct {
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
	// Repos maps a monitored service name to its "owner/repo".
	Repos map[string]string

	http *http.Client
	mu   sync.RWMutex
	byID map[string]Info
}

func NewChecker(st *store.Store, log *slog.Logger, interval time.Duration, repos map[string]string) *Checker {
	return &Checker{
		Store: st, Log: log, Interval: interval, Repos: repos,
		http: &http.Client{Timeout: 10 * time.Second},
		byID: map[string]Info{},
	}
}

// Get returns the cached update info for a service, if checked.
func (c *Checker) Get(service string) (Info, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	i, ok := c.byID[service]
	return i, ok
}

func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	c.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.pass(ctx)
		}
	}
}

func (c *Checker) pass(ctx context.Context) {
	// Current versions come from the last health probe.
	health, err := c.Store.ListServiceHealth(ctx)
	if err != nil {
		c.Log.Warn("update check: read health", "err", err)
		return
	}
	current := map[string]string{}
	for _, h := range health {
		current[h.Service] = h.Version
	}

	for service, repo := range c.Repos {
		cur := current[service]
		latest, err := c.latestTag(ctx, repo)
		if err != nil {
			c.Log.Debug("update check: github", "repo", repo, "err", err)
			continue
		}
		// A running version of "" or "main"/"dev" (latest-tracking build) isn't
		// a comparable release tag — record latest but don't flag an update.
		avail := cur != "" && isSemver(cur) && isSemver(latest) && semverLess(cur, latest)
		c.mu.Lock()
		c.byID[service] = Info{Current: cur, Latest: latest, UpdateAvailable: avail}
		c.mu.Unlock()
	}
}

// latestTag returns the highest semver git tag in the repo. These repos tag
// releases (vX.Y.Z) but don't cut GitHub Releases, so /releases/latest 404s —
// the tags list is the source of truth. One page (100 tags) is plenty.
func (c *Checker) latestTag(ctx context.Context, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/tags?per_page=100", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", &httpErr{resp.StatusCode}
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return "", err
	}
	best := ""
	for _, t := range tags {
		if !isSemver(t.Name) {
			continue
		}
		if best == "" || semverLess(best, t.Name) {
			best = t.Name
		}
	}
	if best == "" {
		return "", &httpErr{404}
	}
	return best, nil
}

type httpErr struct{ code int }

func (e *httpErr) Error() string { return "github status " + strconv.Itoa(e.code) }

func isSemver(v string) bool {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		p = strings.SplitN(p, "-", 2)[0] // drop pre-release suffix
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// semverLess reports whether a < b for vMAJOR.MINOR[.PATCH] tags.
func semverLess(a, b string) bool {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		p = strings.SplitN(p, "-", 2)[0]
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
