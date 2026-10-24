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
	"github.com/dhanvin018/client-directory/internal/model"
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

// AccessTokens signs the short-lived access tokens that name a session. The
// same implementation backs middleware.TokenVerifier, so a token issued here
// is accepted by Authenticate.
type AccessTokens interface {
	Issue(ctx context.Context, peerID, sessionID uuid.UUID) (token string, expiresAt time.Time, err error)
	// SessionID returns the session a token belongs to, checking the
	// signature but not the expiry (logout works with an expired token).
	SessionID(ctx context.Context, token string) (uuid.UUID, error)
}

type AuthConfig struct {
	Providers map[string]OAuthProvider // keyed by the {provider} path segment, e.g. "google"
	Tokens    AccessTokens
	Sessions  *service.SessionService // refresh tokens and logout, stored in the database
	Peers     *service.PeerService

	// Desktop handles sign-in for desktop apps (loopback redirect + PKCE),
	// which end with a one-time code instead of a session cookie.
	Desktop *service.DesktopLoginService

	// CookieName must equal middleware.AuthConfig.CookieName, so the session
	// cookie set here is the one Authenticate reads.
	CookieName string
	// RefreshCookieName holds a browser's refresh token (default "refresh").
	// It is scoped to /auth, so it is only sent to refresh and logout.
	RefreshCookieName string
	CookieSecure      bool          // true in production (HTTPS)
	CookieSameSite    http.SameSite // zero value means Lax

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
	case cfg.Tokens == nil:
		panic("handler: AuthConfig.Tokens is required")
	case cfg.Sessions == nil:
		panic("handler: AuthConfig.Sessions is required")
	case cfg.Peers == nil:
		panic("handler: AuthConfig.Peers is required")
	case cfg.Desktop == nil:
		panic("handler: AuthConfig.Desktop is required")
	case cfg.CookieName == "":
		panic("handler: AuthConfig.CookieName is required")
	case cfg.PostLoginRedirect == "":
		panic("handler: AuthConfig.PostLoginRedirect is required")
	}
	sameSite := cfg.CookieSameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	if cfg.RefreshCookieName == "" {
		cfg.RefreshCookieName = "refresh"
	}
	return &AuthHandler{base: newBase(cfg.Logger), cfg: cfg, sameSite: sameSite}
}

// Login starts the flow: set a one-time state cookie, then redirect to the
// provider.
//
// Browsers call it with no query. Desktop apps add redirect_uri (a loopback
// URI), code_challenge, code_challenge_method=S256 and their own state; the
// sign-in then ends at that loopback URI with a one-time code instead of a
// session cookie (see service.DesktopLoginService).
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
	if q := r.URL.Query(); q.Has("redirect_uri") {
		err := h.cfg.Desktop.Begin(r.Context(), state, service.DesktopLoginRequest{
			RedirectURI:         q.Get("redirect_uri"),
			CodeChallenge:       q.Get("code_challenge"),
			CodeChallengeMethod: q.Get("code_challenge_method"),
			AppState:            q.Get("state"),
		})
		if err != nil {
			h.fail(w, r, err)
			return
		}
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
// the peer (sign up on first sign-in), then either start a browser session
// (cookie + redirect to the client) or, for a desktop sign-in, send a one-time
// code to the app's loopback URI.
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
	desktop, err := h.cfg.Desktop.Take(r.Context(), q.Get("state"))
	switch {
	case errors.Is(err, service.ErrNotFound):
		desktop = nil // a browser sign-in
	case err != nil:
		h.fail(w, r, err)
		return
	}

	if q.Get("error") != "" { // the user declined at the provider
		if desktop != nil {
			h.redirectToApp(w, r, desktop, url.Values{"error": {"access_denied"}})
		} else {
			h.redirectWithError(w, r, "access_denied")
		}
		return
	}
	code := q.Get("code")
	if code == "" {
		if desktop != nil {
			h.redirectToApp(w, r, desktop, url.Values{"error": {"invalid_request"}})
		} else {
			writeError(w, http.StatusBadRequest, "missing authorization code")
		}
		return
	}

	identity, err := provider.Exchange(r.Context(), code)
	if err != nil || identity.Subject == "" {
		h.log.WarnContext(r.Context(), "oauth exchange failed",
			"provider", providerName,
			"error", err,
			"request_id", middleware.RequestIDFromContext(r.Context()))
		if desktop != nil {
			h.redirectToApp(w, r, desktop, url.Values{"error": {"server_error"}})
		} else {
			writeError(w, http.StatusBadGateway, "could not complete sign-in with the provider")
		}
		return
	}

	peer, _, err := h.cfg.Peers.FindOrCreateByAuth(r.Context(), providerName, identity.Subject, displayName(identity))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if desktop != nil {
		appCode, err := h.cfg.Desktop.IssueCode(r.Context(), peer.PeerID, *desktop)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.redirectToApp(w, r, desktop, url.Values{"code": {appCode}})
		return
	}

	t, err := h.startSession(r.Context(), peer.PeerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.setSessionCookies(w, t)
	http.Redirect(w, r, h.cfg.PostLoginRedirect, http.StatusFound)
}

// redirectToApp sends the browser to the desktop app's loopback URI with
// params plus the app's own state, so the app can match the response to the
// sign-in it started (RFC 6749 section 4.1.2).
func (h *AuthHandler) redirectToApp(w http.ResponseWriter, r *http.Request, login *service.DesktopLogin, params url.Values) {
	params.Set("state", login.AppState)
	http.Redirect(w, r, login.RedirectURI+"?"+params.Encode(), http.StatusFound)
}

type tokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"codeVerifier"`
	RedirectURI  string `json:"redirectUri"`
}

// tokenResponse is returned by /auth/token and /auth/refresh. A browser
// refreshing with its cookie gets only the expiry times: its tokens stay in
// HttpOnly cookies, out of reach of page scripts.
type tokenResponse struct {
	AccessToken      string      `json:"accessToken,omitempty"`
	TokenType        string      `json:"tokenType,omitempty"`
	ExpiresAt        time.Time   `json:"expiresAt"`
	RefreshToken     string      `json:"refreshToken,omitempty"`
	RefreshExpiresAt time.Time   `json:"refreshExpiresAt"`
	Peer             *model.Peer `json:"peer,omitempty"`
}

// sessionTokens is a session's refresh token plus a fresh access token.
type sessionTokens struct {
	access, refresh       string
	accessExp, refreshExp time.Time
}

func (t *sessionTokens) response() tokenResponse {
	return tokenResponse{
		AccessToken: t.access, TokenType: "Bearer", ExpiresAt: t.accessExp,
		RefreshToken: t.refresh, RefreshExpiresAt: t.refreshExp,
	}
}

func (h *AuthHandler) startSession(ctx context.Context, peerID uuid.UUID) (*sessionTokens, error) {
	sess, err := h.cfg.Sessions.Start(ctx, peerID)
	if err != nil {
		return nil, err
	}
	return h.accessFor(ctx, sess)
}

func (h *AuthHandler) accessFor(ctx context.Context, sess *service.NewSession) (*sessionTokens, error) {
	access, exp, err := h.cfg.Tokens.Issue(ctx, sess.PeerID, sess.SessionID)
	if err != nil {
		return nil, err
	}
	return &sessionTokens{access: access, accessExp: exp, refresh: sess.RefreshToken, refreshExp: sess.ExpiresAt}, nil
}

// Token finishes a desktop sign-in: it trades the one-time code and the PKCE
// verifier for an access token (sent as a bearer token) and a refresh token
// (kept in the OS keychain). Any problem with the code is the same 400.
func (h *AuthHandler) Token(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	peerID, err := h.cfg.Desktop.Redeem(r.Context(), req.Code, req.CodeVerifier, req.RedirectURI)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.startSession(r.Context(), peerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	peer, err := h.cfg.Peers.Get(r.Context(), peerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resp := t.response()
	resp.Peer = peer
	w.Header().Set("Cache-Control", "no-store") // tokens must not be cached (RFC 6749 section 5.1)
	writeJSON(w, http.StatusOK, resp)
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// Refresh spends a refresh token and returns a new access token and a new
// refresh token. Desktop apps send {"refreshToken": ...}; browsers send no
// body and the refresh cookie, and get new cookies. A bad token is 401: sign
// in again.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	token, fromCookie, ok := h.refreshToken(w, r)
	if !ok {
		return
	}
	sess, err := h.cfg.Sessions.Refresh(r.Context(), token)
	if err != nil {
		if fromCookie && errors.Is(err, service.ErrUnauthenticated) {
			h.clearSessionCookies(w)
		}
		h.fail(w, r, err)
		return
	}
	t, err := h.accessFor(r.Context(), sess)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if fromCookie {
		h.setSessionCookies(w, t)
		writeJSON(w, http.StatusOK, tokenResponse{ExpiresAt: t.accessExp, RefreshExpiresAt: t.refreshExp})
		return
	}
	writeJSON(w, http.StatusOK, t.response())
}

// Logout ends the session in the database and clears the cookies. The
// session is found by the refresh token (body or cookie) or else by the
// access token, which may be expired. Logging out twice is fine.
//
// Access tokens already issued stay valid until they expire (minutes); the
// session can no longer be refreshed.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	token, _, ok := h.refreshToken(w, r)
	if !ok {
		return
	}
	var err error
	if token != "" {
		err = h.cfg.Sessions.End(r.Context(), token)
	} else if access := middleware.TokenFromRequest(r, h.cfg.CookieName); access != "" {
		if sid, perr := h.cfg.Tokens.SessionID(r.Context(), access); perr == nil {
			err = h.cfg.Sessions.EndByID(r.Context(), sid)
		}
	}
	h.endedSessions(w, r, err)
}

// LogoutAll ends every session of the caller, on every device.
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	h.endedSessions(w, r, h.cfg.Sessions.EndAll(r.Context(), actor))
}

func (h *AuthHandler) endedSessions(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		// Don't claim the user is logged out if the session is still live.
		h.log.ErrorContext(r.Context(), "ending session failed",
			"error", err,
			"request_id", middleware.RequestIDFromContext(r.Context()))
		writeError(w, http.StatusServiceUnavailable, "could not end the session, please try again")
		return
	}
	h.clearSessionCookies(w)
	noContent(w)
}

// refreshToken reads the refresh token from a JSON body, else from the
// refresh cookie. ok is false when it already wrote an error response.
func (h *AuthHandler) refreshToken(w http.ResponseWriter, r *http.Request) (token string, fromCookie, ok bool) {
	if r.ContentLength != 0 {
		var req refreshRequest
		if !decodeJSON(w, r, &req) {
			return "", false, false
		}
		if req.RefreshToken != "" {
			return req.RefreshToken, false, true
		}
	}
	if c, err := r.Cookie(h.cfg.RefreshCookieName); err == nil && c.Value != "" {
		return c.Value, true, true
	}
	return "", false, true
}

// setSessionCookies stores a browser's tokens: the access token for every
// request, the refresh token only for /auth (refresh and logout).
func (h *AuthHandler) setSessionCookies(w http.ResponseWriter, t *sessionTokens) {
	h.setCookie(w, h.cfg.CookieName, t.access, "/", t.accessExp)
	h.setCookie(w, h.cfg.RefreshCookieName, t.refresh, "/auth", t.refreshExp)
}

func (h *AuthHandler) clearSessionCookies(w http.ResponseWriter) {
	h.setCookie(w, h.cfg.CookieName, "", "/", time.Time{})
	h.setCookie(w, h.cfg.RefreshCookieName, "", "/auth", time.Time{})
}

// setCookie sets an HttpOnly cookie; a zero expiry deletes it.
func (h *AuthHandler) setCookie(w http.ResponseWriter, name, value, path string, expires time.Time) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: h.sameSite,
	}
	if expires.IsZero() {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
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
