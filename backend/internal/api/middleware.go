package api

import (
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/pburkhalter/journarr/internal/audit"
)

// withActor tags the request context with who is calling, for the actions
// audit (OIDC user, api-token, or anonymous in open LAN mode).
func (s *server) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(audit.WithActor(r.Context(), s.Auth.Actor(r))))
	})
}

// limitBody caps request bodies; a larger body fails the JSON decode.
func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrf rejects cross-site state-changing requests. Two independent checks:
// Go's CrossOriginProtection (Sec-Fetch-Site, else Origin vs Host, with the
// public URL as trusted origin behind a proxy), and — for requests that ride
// on a cookie rather than a bearer token — a JSON content type, which a
// cross-site form or a no-cors fetch cannot send without a preflight. Only
// /api is wrapped; /webhook/* (TrueNAS posts with ?token=) and /mcp (bearer)
// are not browser endpoints.
func (s *server) csrf() func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if u, err := url.Parse(s.PublicURL); err == nil && u.Scheme != "" && u.Host != "" {
		if err := cop.AddTrustedOrigin(u.Scheme + "://" + u.Host); err != nil {
			s.Log.Warn("csrf: public url not usable as trusted origin", "url", s.PublicURL, "err", err)
		}
	}
	return func(next http.Handler) http.Handler {
		guarded := cop.Handler(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !safeMethod(r.Method) && !hasBearer(r) && !isJSON(r) {
				writeJSONStatus(w, http.StatusUnsupportedMediaType,
					map[string]string{"error": "Content-Type must be application/json"})
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}
