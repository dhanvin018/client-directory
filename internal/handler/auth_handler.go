package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/service"
)

const (
	stateCookieName = "oauth_state"
	stateTTL        = 10 * time.Minute
	maxUserNameLen  = 64 // matches the service's userName limit
)

// Identity is what an OAuth provider tells us about the signed-in user.
type Identity struct {
	Subject string // provider's stable user ID; required
	Name    string // display name; optional
	Email   string // used only as a display-name fallback
}

// OAuthProvider is one third-party identity provider (Google, GitHub, ...).
type OAuthProvider interface {
	// AuthCodeURL returns the provider URL to send the user to.
	AuthCodeURL(state string) string
	// Exchange trades the authorization code for the user's identity.
	Exchange(ctx context.Context, code string) (Identity, error)
}

// SessionManager creates and revokes directory sessions. The same store also
// backs middleware.TokenVerifier (Verify), so a token issued here is accepted
// by Authenticate. How tokens are stored is up to the implementation.
type SessionManager interface {
	Issue(ctx context.Context, peerID uuid.UUID) (token string, expiresAt time.Time, err error)
	// Revoke ends a session. Return middleware.ErrInvalidToken (or nil) for a
	// token that is already gone; any other error means "could not revoke".
	Revoke(ctx context.Context, token string) error
}

type AuthConfig struct {
	Providers map[string]OAuthProvider // keyed by the {provider} path segment, e.g. "google"
	Sessions  SessionManager
	Peers     *service.PeerService

	// CookieName must equal middleware.AuthConfig.CookieName, so the session
	// cookie set here is the one Authenticate reads.
	CookieName     string
	CookieSecure   bool          // true in production (HTTPS)
	CookieSameSite http.SameSite // zero value means Lax

	// PostLoginRedirect is where the browser lands after sign-in, e.g. the
	// React app's URL. On a denied sign-in it gets ?auth_error=access_denied.
	PostLoginRedirect string

	Logger *slog.Logger
}

type AuthHandler struct {
	base
	cfg      AuthConfig
	sameSite http.SameSite
}

func NewAuthHandler(cfg AuthConfig) *AuthHandler {
	switch {
	case len(cfg.Providers) == 0:
		panic("handler: AuthConfig.Providers is required")
	case cfg.Sessions == nil:
		panic("handler: AuthConfig.Sessions is required")
	case cfg.Peers == nil:
		panic("handler: AuthConfig.Peers is required")
	case cfg.CookieName == "":
		panic("handler: AuthConfig.CookieName is required")
	case cfg.PostLoginRedirect == "":
		panic("handler: AuthConfig.PostLoginRedirect is required")
	}
	sameSite := cfg.CookieSameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	return &AuthHandler{base: newBase(cfg.Logger), cfg: cfg, sameSite: sameSite}
}

// Login starts the flow: set a one-time state cookie, then redirect to the provider.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	provider, ok := h.cfg.Providers[r.PathValue("provider")]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}

	state, err := randomToken(32)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/auth",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, // Lax so it is sent on the provider's redirect back
	})
	http.Redirect(w, r, provider.AuthCodeURL(state), http.StatusFound)
}

// Callback finishes the flow: check state, exchange the code, find or create
// the peer (sign up on first sign-in), start a session, redirect to the client.
func (h *AuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("provider")
	provider, ok := h.cfg.Providers[providerName]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	if !h.checkState(w, r) {
		return
	}

	q := r.URL.Query()
	if q.Get("error") != "" { // the user declined at the provider
		h.redirectWithError(w, r, "access_denied")
		return
	}
	code := q.Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	identity, err := provider.Exchange(r.Context(), code)
	if err != nil || identity.Subject == "" {
		h.log.WarnContext(r.Context(), "oauth exchange failed",
			"provider", providerName,
			"error", err,
			"request_id", middleware.RequestIDFromContext(r.Context()))
		writeError(w, http.StatusBadGateway, "could not complete sign-in with the provider")
		return
	}

	peer, _, err := h.cfg.Peers.FindOrCreateByAuth(r.Context(), providerName, identity.Subject, displayName(identity))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	token, expiresAt, err := h.cfg.Sessions.Issue(r.Context(), peer.PeerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.cfg.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.sameSite,
	})
	http.Redirect(w, r, h.cfg.PostLoginRedirect, http.StatusFound)
}

// Logout revokes the caller's session and clears the cookie.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if token := middleware.TokenFromRequest(r, h.cfg.CookieName); token != "" {
		err := h.cfg.Sessions.Revoke(r.Context(), token)
		if err != nil && !errors.Is(err, middleware.ErrInvalidToken) {
			// Don't claim the user is logged out if the session is still live.
			h.log.ErrorContext(r.Context(), "session revoke failed",
				"error", err,
				"request_id", middleware.RequestIDFromContext(r.Context()))
			writeError(w, http.StatusServiceUnavailable, "could not end the session, please try again")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.cfg.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.sameSite,
	})
	noContent(w)
}

// checkState verifies the state query parameter against the one-time cookie
// set by Login (CSRF protection for the OAuth flow) and clears the cookie.
func (h *AuthHandler) checkState(w http.ResponseWriter, r *http.Request) bool {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Path:     "/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	cookie, err := r.Cookie(stateCookieName)
	got := r.URL.Query().Get("state")
	if err != nil || got == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(got)) != 1 {
		writeError(w, http.StatusBadRequest, "invalid or expired sign-in state")
		return false
	}
	return true
}

func (h *AuthHandler) redirectWithError(w http.ResponseWriter, r *http.Request, code string) {
	u, err := url.Parse(h.cfg.PostLoginRedirect)
	if err != nil {
		writeError(w, http.StatusBadRequest, "sign-in was cancelled")
		return
	}
	q := u.Query()
	q.Set("auth_error", code)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// displayName picks a user name for a new peer: the provider's name, else the
// local part of the email, else a placeholder.
func displayName(id Identity) string {
	name := strings.TrimSpace(id.Name)
	if name == "" {
		if local, _, ok := strings.Cut(id.Email, "@"); ok {
			name = strings.TrimSpace(local)
		}
	}
	if name == "" {
		name = "user"
	}
	if r := []rune(name); len(r) > maxUserNameLen {
		name = string(r[:maxUserNameLen])
	}
	return name
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
