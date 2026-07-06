package clients

import (
	"context"
	"net/http"
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

// RecentlyAdded lists the newest Movies and Episodes with provider ids + path.
func (c *Jellyfin) RecentlyAdded(ctx context.Context, limit int) ([]JellyItem, error) {
	base := c.BaseURL + "/Items"
	if c.UserID != "" {
		base = c.BaseURL + "/Users/" + c.UserID + "/Items"
	}
	url := base + "?SortBy=DateCreated&SortOrder=Descending&Recursive=true" +
		"&IncludeItemTypes=Movie,Episode&Fields=ProviderIds,Path,DateCreated" +
		"&Limit=" + itoa(limit)
	var out struct {
		Items []JellyItem `json:"Items"`
	}
	if _, err := getJSON(ctx, c.HTTP, url, c.headers(), &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SeriesTvdbID resolves a series' TVDB id from its Jellyfin item id.
func (c *Jellyfin) SeriesTvdbID(ctx context.Context, seriesID string) (int64, error) {
	url := c.BaseURL + "/Items/" + seriesID
	if c.UserID != "" {
		url = c.BaseURL + "/Users/" + c.UserID + "/Items/" + seriesID
	}
	var out struct {
		ProviderIds map[string]string `json:"ProviderIds"`
	}
	if _, err := getJSON(ctx, c.HTTP, url, c.headers(), &out); err != nil {
		return 0, err
	}
	return parseProviderID(out.ProviderIds, "Tvdb"), nil
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
