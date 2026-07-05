// Package auth implements optional OIDC SSO (built for Pocket ID, works with
// any spec-compliant provider: Authentik, Authelia, Keycloak, …).
//
// Auth code flow + PKCE via golang.org/x/oauth2, discovery + ID-token
// verification via coreos/go-oidc, sessions via gorilla/sessions cookies.
// With no issuer configured everything is a no-op and Journarr stays open
// (LAN mode).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"
)

const (
	sessionName = "journarr_session"
	flowName    = "journarr_oauth"
	userKey     = "user"
)

type Config struct {
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	PublicURL     string // external base URL, e.g. https://journarr.example.com
	Scopes        []string
	AllowedGroups []string // empty = anyone the provider authenticates
	SessionSecret string
	SessionMaxAge time.Duration
}

type User struct {
	Subject string `json:"sub"`
	Email   string `json:"email,omitempty"`
	Name    string `json:"name,omitempty"`
	Picture string `json:"picture,omitempty"`
}

func init() { gob.Register(&User{}) }

type Auth struct {
	cfg   Config
	log   *slog.Logger
	store *sessions.CookieStore

	// OIDC discovery happens lazily so Journarr still boots when the
	// provider is down; the first login attempt retries it.
	mu       sync.Mutex
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func New(cfg Config, log *slog.Logger) *Auth {
	a := &Auth{cfg: cfg, log: log}
	if !a.Enabled() {
		return a
	}
	// Two independent keys derived from one secret: HMAC signing + AES
	// encryption of the cookie payload.
	authKey := sha256.Sum256([]byte(cfg.SessionSecret + ":sign"))
	encKey := sha256.Sum256([]byte(cfg.SessionSecret + ":encrypt"))
	a.store = sessions.NewCookieStore(authKey[:], encKey[:])
	a.store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   int(cfg.SessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(cfg.PublicURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	}
	return a
}

func (a *Auth) Enabled() bool { return a.cfg.IssuerURL != "" }

func (a *Auth) init(r *http.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return nil
	}
	provider, err := oidc.NewProvider(r.Context(), a.cfg.IssuerURL)
	if err != nil {
		return fmt.Errorf("oidc discovery %s: %w", a.cfg.IssuerURL, err)
	}
	a.provider = provider
	a.verifier = provider.Verifier(&oidc.Config{ClientID: a.cfg.ClientID})
	a.oauth = &oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  strings.TrimRight(a.cfg.PublicURL, "/") + "/auth/callback",
		Scopes:       a.cfg.Scopes,
	}
	return nil
}

// Routes mounts /auth/login, /auth/callback and /auth/logout.
func (a *Auth) Routes(mux interface {
	Get(pattern string, h http.HandlerFunc)
}) {
	mux.Get("/auth/login", a.handleLogin)
	mux.Get("/auth/callback", a.handleCallback)
	mux.Get("/auth/logout", a.handleLogout)
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err := a.init(r); err != nil {
		a.log.Error("auth: provider unavailable", "err", err)
		http.Error(w, "identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	state := randomHex(16)
	pkce := oauth2.GenerateVerifier()

	flow, _ := a.store.New(r, flowName)
	flow.Options = &sessions.Options{
		Path: "/", MaxAge: 600, HttpOnly: true,
		Secure:   strings.HasPrefix(a.cfg.PublicURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	}
	flow.Values["state"] = state
	flow.Values["verifier"] = pkce
	flow.Values["rd"] = safeRedirect(r.URL.Query().Get("rd"))
	if err := flow.Save(r, w); err != nil {
		a.log.Error("auth: save flow session", "err", err)
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(pkce)), http.StatusFound)
}

func (a *Auth) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err := a.init(r); err != nil {
		http.Error(w, "identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	flow, err := a.store.Get(r, flowName)
	if err != nil || flow.IsNew {
		http.Error(w, "login flow expired — try again", http.StatusBadRequest)
		return
	}
	state, _ := flow.Values["state"].(string)
	pkce, _ := flow.Values["verifier"].(string)
	rd, _ := flow.Values["rd"].(string)

	// Single-use: drop the flow cookie regardless of outcome.
	flow.Options.MaxAge = -1
	_ = flow.Save(r, w)

	if state == "" || r.URL.Query().Get("state") != state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "provider error: "+errMsg, http.StatusBadRequest)
		return
	}

	token, err := a.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(pkce))
	if err != nil {
		a.log.Error("auth: code exchange failed", "err", err)
		http.Error(w, "code exchange failed", http.StatusBadGateway)
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusBadGateway)
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil {
		a.log.Error("auth: id token verification failed", "err", err)
		http.Error(w, "invalid id token", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email             string   `json:"email"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Picture           string   `json:"picture"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "cannot parse claims", http.StatusBadGateway)
		return
	}
	if len(a.cfg.AllowedGroups) > 0 && !overlaps(claims.Groups, a.cfg.AllowedGroups) {
		a.log.Warn("auth: user not in allowed groups", "sub", idToken.Subject, "groups", claims.Groups)
		http.Error(w, "access denied: not in an allowed group", http.StatusForbidden)
		return
	}

	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	sess, _ := a.store.New(r, sessionName)
	sess.Values[userKey] = &User{
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    name,
		Picture: claims.Picture,
	}
	if err := sess.Save(r, w); err != nil {
		a.log.Error("auth: save session", "err", err)
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	a.log.Info("auth: login", "sub", idToken.Subject, "email", claims.Email)
	if rd == "" {
		rd = "/"
	}
	http.Redirect(w, r, rd, http.StatusFound)
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if a.Enabled() {
		sess, _ := a.store.Get(r, sessionName)
		sess.Options.MaxAge = -1
		_ = sess.Save(r, w)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// UserFrom returns the logged-in user, or nil. Second return is false only
// when auth is enabled and the request has no valid session.
func (a *Auth) UserFrom(r *http.Request) (*User, bool) {
	if !a.Enabled() {
		return nil, true
	}
	sess, err := a.store.Get(r, sessionName)
	if err != nil || sess.IsNew {
		return nil, false
	}
	u, ok := sess.Values[userKey].(*User)
	if !ok {
		return nil, false
	}
	return u, true
}

// RequireAPI rejects unauthenticated API calls with 401 JSON.
func (a *Auth) RequireAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.UserFrom(r); !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireBrowser redirects unauthenticated page loads to the login flow.
func (a *Auth) RequireBrowser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.UserFrom(r); !ok {
			http.Redirect(w, r, "/auth/login?rd="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		next(w, r)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// safeRedirect only allows same-origin relative paths — no open redirects.
func safeRedirect(rd string) string {
	if rd == "" || !strings.HasPrefix(rd, "/") || strings.HasPrefix(rd, "//") {
		return "/"
	}
	return rd
}

func overlaps(have, want []string) bool {
	for _, w := range want {
		if slices.Contains(have, w) {
			return true
		}
	}
	return false
}
