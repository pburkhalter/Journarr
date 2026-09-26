package clients

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Jellyfin struct {
	BaseURL string
	APIKey  string
	UserID  string // optional; some deployments require a user context for /Items
	HTTP    *http.Client
}

func NewJellyfin(baseURL, apiKey string, timeout time.Duration) *Jellyfin {
	return &Jellyfin{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: newHTTP(timeout)}
}

// JellyItem is a tolerant subset of a library item.
type JellyItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"` // Movie|Episode
	Path              string            `json:"Path"`
	ProviderIds       map[string]string `json:"ProviderIds"`
	SeriesID          string            `json:"SeriesId"`
	SeriesName        string            `json:"SeriesName"`
	ParentIndexNumber *int64            `json:"ParentIndexNumber"` // season
	IndexNumber       *int64            `json:"IndexNumber"`       // episode
	IndexNumberEnd    *int64            `json:"IndexNumberEnd"`    // last episode of a multi-ep file
	DateCreated       string            `json:"DateCreated"`
}

// itemsBase is the collection endpoint: user-scoped when a user id is set,
// otherwise the api-key-only form.
func (c *Jellyfin) itemsBase() string {
	if c.UserID != "" {
		return c.BaseURL + "/Users/" + c.UserID + "/Items"
	}
	return c.BaseURL + "/Items"
}

// RecentlyAdded lists the newest Movies and Episodes with provider ids + path.
func (c *Jellyfin) RecentlyAdded(ctx context.Context, limit int) ([]JellyItem, error) {
	return c.ItemsByDateCreated(ctx, 0, limit)
}

// ItemsByDateCreated pages Movies and Episodes newest-first, so a caller can
// walk back to a cursor instead of hoping the newest N cover everything.
func (c *Jellyfin) ItemsByDateCreated(ctx context.Context, startIndex, limit int) ([]JellyItem, error) {
	url := c.itemsBase() + "?SortBy=DateCreated&SortOrder=Descending&Recursive=true" +
		"&IncludeItemTypes=Movie,Episode&Fields=ProviderIds,Path,DateCreated" +
		"&StartIndex=" + itoa(startIndex) + "&Limit=" + itoa(limit)
	var out struct {
		Items []JellyItem `json:"Items"`
	}
	if _, err := getJSON(ctx, c.HTTP, url, c.headers(), &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SeriesTvdbID resolves a series' TVDB id from its Jellyfin item id.
//
// It queries the collection endpoint with Ids= rather than /Items/{id}: on
// Jellyfin 10.11 the single-item route answers 400 to an api-key-only call
// (no user context), which silently disabled episode matching for months.
// The collection form works in both modes.
func (c *Jellyfin) SeriesTvdbID(ctx context.Context, seriesID string) (int64, error) {
	url := c.itemsBase() + "?Ids=" + seriesID + "&Fields=ProviderIds"
	var out struct {
		Items []struct {
			ProviderIds map[string]string `json:"ProviderIds"`
		} `json:"Items"`
	}
	if _, err := getJSON(ctx, c.HTTP, url, c.headers(), &out); err != nil {
		return 0, err
	}
	if len(out.Items) == 0 {
		return 0, fmt.Errorf("series %s not found", seriesID)
	}
	return parseProviderID(out.Items[0].ProviderIds, "Tvdb"), nil
}

// ItemsByName finds Series and Movies whose name matches term, with their
// paths — used to check that a removed title left the library.
func (c *Jellyfin) ItemsByName(ctx context.Context, term string) ([]JellyItem, error) {
	u := c.itemsBase() + "?Recursive=true&IncludeItemTypes=Series,Movie&Fields=Path,ProviderIds&Limit=50" +
		"&SearchTerm=" + url.QueryEscape(term)
	var out struct {
		Items []JellyItem `json:"Items"`
	}
	if _, err := getJSON(ctx, c.HTTP, u, c.headers(), &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// RefreshLibrary triggers a full library scan.
func (c *Jellyfin) RefreshLibrary(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/Library/Refresh", nil)
	if err != nil {
		return err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode}
	}
	return nil
}

// parseProviderID reads a provider id case-insensitively (Jellyfin keys vary).
func parseProviderID(ids map[string]string, key string) int64 {
	for k, v := range ids {
		if strings.EqualFold(k, key) {
			n := int64(0)
			for _, ch := range v {
				if ch < '0' || ch > '9' {
					return 0
				}
				n = n*10 + int64(ch-'0')
			}
			return n
		}
	}
	return 0
}

type APIError struct {
	Status int
}

func (e *APIError) Error() string { return "jellyfin status " + itoa(e.Status) }

func (c *Jellyfin) headers() map[string]string {
	h := map[string]string{}
	if c.APIKey != "" {
		h["X-Emby-Token"] = c.APIKey
	}
	return h
}

func (c *Jellyfin) CheckHealth(ctx context.Context) HealthResult {
	var body struct {
		Version    string `json:"Version"`
		ServerName string `json:"ServerName"`
	}
	// Public endpoint — works without a token, token sent anyway when set.
	lat, err := getJSON(ctx, c.HTTP, c.BaseURL+"/System/Info/Public", c.headers(), &body)
	if err != nil {
		return down(lat, err)
	}
	return HealthResult{
		Status:  "up",
		Latency: lat,
		Version: body.Version,
		Detail:  map[string]any{"server_name": body.ServerName},
	}
}

// JellySession is one active Jellyfin playback session (someone streaming).
type JellySession struct {
	User        string `json:"user"`
	Client      string `json:"client"`
	Device      string `json:"device"`
	Title       string `json:"title"`
	MediaType   string `json:"media_type"` // Movie|Episode|Audio|...
	PositionSec int64  `json:"position_sec"`
	RuntimeSec  int64  `json:"runtime_sec"`
	Paused      bool   `json:"paused"`
	PlayMethod  string `json:"play_method"` // DirectPlay|DirectStream|Transcode
	RemoteIP    string `json:"remote_ip,omitempty"`
	// For matching a session to a title (not sent to the UI).
	Path       string `json:"-"`
	SeriesName string `json:"-"`
	ItemName   string `json:"-"`
}

// Sessions returns the currently-playing Jellyfin sessions (those with a
// NowPlayingItem) — powers the "who's streaming / safe to restart" view. It
// derives entirely from the registered Jellyfin instance; nothing hardcoded.
func (c *Jellyfin) Sessions(ctx context.Context) ([]JellySession, error) {
	var raw []struct {
		UserName       string `json:"UserName"`
		Client         string `json:"Client"`
		DeviceName     string `json:"DeviceName"`
		RemoteEndPoint string `json:"RemoteEndPoint"`
		NowPlayingItem *struct {
			Name              string `json:"Name"`
			Type              string `json:"Type"`
			Path              string `json:"Path"`
			SeriesName        string `json:"SeriesName"`
			ParentIndexNumber *int   `json:"ParentIndexNumber"`
			IndexNumber       *int   `json:"IndexNumber"`
			RunTimeTicks      int64  `json:"RunTimeTicks"`
		} `json:"NowPlayingItem"`
		PlayState struct {
			PositionTicks int64  `json:"PositionTicks"`
			IsPaused      bool   `json:"IsPaused"`
			PlayMethod    string `json:"PlayMethod"`
		} `json:"PlayState"`
	}
	// ActiveWithinSeconds trims idle clients; only NowPlaying sessions matter.
	if _, err := getJSON(ctx, c.HTTP, c.BaseURL+"/Sessions?ActiveWithinSeconds=960", c.headers(), &raw); err != nil {
		return nil, err
	}
	out := []JellySession{}
	for _, s := range raw {
		np := s.NowPlayingItem
		if np == nil {
			continue // idle client, not streaming
		}
		title := np.Name
		if np.Type == "Episode" && np.SeriesName != "" {
			se := ""
			if np.ParentIndexNumber != nil && np.IndexNumber != nil {
				se = fmt.Sprintf(" S%02dE%02d", *np.ParentIndexNumber, *np.IndexNumber)
			}
			title = fmt.Sprintf("%s%s — %s", np.SeriesName, se, np.Name)
		}
		out = append(out, JellySession{
			User: s.UserName, Client: s.Client, Device: s.DeviceName,
			Title: title, MediaType: np.Type,
			PositionSec: s.PlayState.PositionTicks / 10_000_000,
			RuntimeSec:  np.RunTimeTicks / 10_000_000,
			Paused:      s.PlayState.IsPaused,
			PlayMethod:  s.PlayState.PlayMethod,
			RemoteIP:    s.RemoteEndPoint,
			Path:        np.Path,
			SeriesName:  np.SeriesName,
			ItemName:    np.Name,
		})
	}
	return out, nil
}
