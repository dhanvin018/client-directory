package handler_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/handler"
)

const appLoopback = "http://127.0.0.1:53682/callback"

// desktopApp plays the desktop app: it owns the PKCE verifier and its state.
type desktopApp struct {
	verifier, challenge, state string
}

func newDesktopApp() desktopApp {
	verifier := strings.Repeat("abcdefghij", 5) // 50 unreserved characters
	sum := sha256.Sum256([]byte(verifier))
	return desktopApp{verifier: verifier, challenge: base64.RawURLEncoding.EncodeToString(sum[:]), state: "app-state-xyz"}
}

func (a desktopApp) loginURL() string {
	q := url.Values{
		"redirect_uri":          {appLoopback},
		"code_challenge":        {a.challenge},
		"code_challenge_method": {"S256"},
		"state":                 {a.state},
	}
	return "/auth/google/login?" + q.Encode()
}

// desktopLogin starts a desktop sign-in and returns the state cookie and the
// state the directory sent to the provider.
func (e *authEnv) desktopLogin(t *testing.T, app desktopApp) (*http.Cookie, string) {
	t.Helper()
	rec := e.do(httptest.NewRequest(http.MethodGet, app.loginURL(), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("desktop login status = %d (%s), want 302", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Host != "provider.test" {
		t.Fatalf("login redirected to %q, want the provider", rec.Header().Get("Location"))
	}
	cookie := findCookie(rec, "oauth_state")
	if cookie == nil || cookie.Value != loc.Query().Get("state") {
		t.Fatal("state cookie missing or not the state sent to the provider")
	}
	return cookie, loc.Query().Get("state")
}

// appRedirect checks the callback sent the browser to the app's loopback URI
// and returns the query it carried.
func appRedirect(t *testing.T, rec *httptest.ResponseRecorder, app desktopApp) url.Values {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("callback status = %d (%s), want 302", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Scheme+"://"+loc.Host+loc.Path != appLoopback {
		t.Fatalf("callback redirected to %q, want the app's loopback URI", rec.Header().Get("Location"))
	}
	if loc.Query().Get("state") != app.state {
		t.Errorf("app state = %q, want %q echoed back", loc.Query().Get("state"), app.state)
	}
	if findCookie(rec, cookieName) != nil {
		t.Error("a desktop sign-in must not set the browser session cookie")
	}
	return loc.Query()
}

func (e *authEnv) token(t *testing.T, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/token", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	return e.do(req)
}

func TestDesktopSignIn(t *testing.T) {
	e := newAuthEnv(t)
	app := newDesktopApp()
	e.provider.identities["google-code"] = handler.Identity{Subject: "sub-1", Name: "Ada Lovelace"}

	cookie, providerState := e.desktopLogin(t, app)
	rec := e.callback(url.Values{"state": {providerState}, "code": {"google-code"}}, cookie)
	q := appRedirect(t, rec, app)
	code := q.Get("code")
	if code == "" {
		t.Fatalf("no code in the app redirect: %v", q)
	}

	rec = e.token(t, map[string]string{"code": code, "codeVerifier": app.verifier, "redirectUri": appLoopback})
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("token response must be Cache-Control: no-store")
	}
	var resp struct {
		AccessToken, TokenType, RefreshToken string
		Peer                                 struct{ PeerID, Handle string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	peerID := e.tokens.peer(resp.AccessToken)
	if peerID == uuid.Nil || resp.TokenType != "Bearer" || resp.RefreshToken == "" ||
		resp.Peer.PeerID != peerID.String() || resp.Peer.Handle != "ada.lovelace" {
		t.Fatalf("token response = %+v", resp)
	}

	// The app refreshes with the token in the body and gets both back in JSON.
	rec = e.post("/auth/refresh", `{"refreshToken":"`+resp.RefreshToken+`"}`)
	var refreshed struct{ AccessToken, RefreshToken string }
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("refresh: status %d (%s)", rec.Code, rec.Body.String())
	}
	if e.tokens.peer(refreshed.AccessToken) != peerID || refreshed.RefreshToken == "" || refreshed.RefreshToken == resp.RefreshToken {
		t.Fatalf("refresh response = %+v", refreshed)
	}
	if findCookie(rec, refreshCookie) != nil {
		t.Error("a body refresh must not set cookies")
	}

	// The code is single use.
	rec = e.token(t, map[string]string{"code": code, "codeVerifier": app.verifier, "redirectUri": appLoopback})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("reused code: status %d, want 400", rec.Code)
	}
}

func TestDesktopTokenNeedsTheVerifier(t *testing.T) {
	e := newAuthEnv(t)
	app := newDesktopApp()
	e.provider.identities["c"] = handler.Identity{Subject: "sub-1"}
	cookie, providerState := e.desktopLogin(t, app)
	code := appRedirect(t, e.callback(url.Values{"state": {providerState}, "code": {"c"}}, cookie), app).Get("code")

	// Someone who intercepted the code but not the verifier gets nothing.
	thief := newDesktopApp()
	thief.verifier = strings.Repeat("zyxwvutsrq", 5)
	rec := e.token(t, map[string]string{"code": code, "codeVerifier": thief.verifier, "redirectUri": appLoopback})
	if rec.Code != http.StatusBadRequest || len(e.store.sessions) != 0 {
		t.Fatalf("stolen code: status %d, %d sessions; want 400 and none", rec.Code, len(e.store.sessions))
	}
}

func TestDesktopLoginRejectsUnsafeRedirects(t *testing.T) {
	for _, redirect := range []string{"https://evil.example/cb", "http://localhost:5000/cb", "http://127.0.0.1/cb"} {
		e := newAuthEnv(t)
		app := newDesktopApp()
		q := url.Values{
			"redirect_uri": {redirect}, "code_challenge": {app.challenge},
			"code_challenge_method": {"S256"}, "state": {app.state},
		}
		rec := e.do(httptest.NewRequest(http.MethodGet, "/auth/google/login?"+q.Encode(), nil))
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Errorf("redirect_uri %q: status %d location %q, want 400 and no redirect",
				redirect, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestDesktopSignInFailuresGoBackToTheApp(t *testing.T) {
	tests := []struct {
		name      string
		query     func(state string) url.Values
		exchange  error
		wantError string
	}{
		{"user declined", func(s string) url.Values { return url.Values{"state": {s}, "error": {"access_denied"}} }, nil, "access_denied"},
		{"no code", func(s string) url.Values { return url.Values{"state": {s}} }, nil, "invalid_request"},
		{"provider failed", func(s string) url.Values { return url.Values{"state": {s}, "code": {"c"}} }, errors.New("boom"), "server_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			e.provider.err = tt.exchange
			app := newDesktopApp()
			cookie, providerState := e.desktopLogin(t, app)
			q := appRedirect(t, e.callback(tt.query(providerState), cookie), app)
			if q.Get("error") != tt.wantError || q.Get("code") != "" {
				t.Errorf("app redirect query = %v, want error=%s and no code", q, tt.wantError)
			}
		})
	}
}

func TestBrowserSignInIgnoresDesktopState(t *testing.T) {
	// A browser sign-in still ends with the cookie, even after a desktop
	// sign-in used the same handler.
	e := newAuthEnv(t)
	e.provider.identities["c"] = handler.Identity{Subject: "sub-1"}
	app := newDesktopApp()
	e.desktopLogin(t, app)

	state := e.login(t)
	rec := e.callback(url.Values{"state": {state.Value}, "code": {"c"}}, state)
	if rec.Header().Get("Location") != postLoginRedirect || findCookie(rec, cookieName) == nil {
		t.Fatalf("browser sign-in: location %q, cookie %v", rec.Header().Get("Location"), findCookie(rec, cookieName))
	}
}
