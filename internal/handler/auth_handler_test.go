package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/handler"
	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

// ---- Fakes ----

// fakeProvider stands in for Google: AuthCodeURL points at a fake host and
// Exchange returns the identity registered for the code.
type fakeProvider struct {
	mu         sync.Mutex
	identities map[string]handler.Identity
	err        error
	exchanged  []string
}

func (p *fakeProvider) AuthCodeURL(state string) string {
	return "https://provider.test/authorize?state=" + url.QueryEscape(state)
}

func (p *fakeProvider) Exchange(ctx context.Context, code string) (handler.Identity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exchanged = append(p.exchanged, code)
	if p.err != nil {
		return handler.Identity{}, p.err
	}
	return p.identities[code], nil
}

type fakeSessions struct {
	mu        sync.Mutex
	issued    map[string]uuid.UUID
	revoked   []string
	revokeErr error
}

func (s *fakeSessions) Issue(ctx context.Context, peerID uuid.UUID) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := "tok-" + uuid.NewString()
	s.issued[token] = peerID
	return token, time.Now().Add(time.Hour), nil
}

func (s *fakeSessions) Revoke(ctx context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, token)
	return s.revokeErr
}

// peerStore is the minimum service.Store the PeerService needs; the other
// repositories are never touched by the auth flow.
type peerStore struct {
	mu    sync.Mutex
	peers map[uuid.UUID]model.Peer
}

func (s *peerStore) Peers() service.PeerRepository         { return s }
func (s *peerStore) Rooms() service.RoomRepository         { return nil }
func (s *peerStore) Documents() service.DocumentRepository { return nil }
func (s *peerStore) Members() service.MemberRepository     { return nil }
func (s *peerStore) Events() service.EventRepository       { return nil }
func (s *peerStore) Invites() service.InviteRepository     { return nil }
func (s *peerStore) Bans() service.BanRepository           { return nil }
func (s *peerStore) WithTx(ctx context.Context, fn func(service.Repos) error) error {
	return fn(s)
}

func (s *peerStore) Get(ctx context.Context, id uuid.UUID) (*model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.peers[id]; ok {
		return &p, nil
	}
	return nil, service.ErrNotFound
}

func (s *peerStore) GetByAuth(ctx context.Context, provider, subject string) (*model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.peers {
		if p.AuthProvider == provider && p.AuthSubject == subject {
			return &p, nil
		}
	}
	return nil, service.ErrNotFound
}

func (s *peerStore) Create(ctx context.Context, in service.NewPeer) (*model.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := model.Peer{PeerID: in.PeerID, Handle: in.Handle, UserName: in.UserName, AuthProvider: in.AuthProvider, AuthSubject: in.AuthSubject}
	s.peers[p.PeerID] = p
	return &p, nil
}

func (s *peerStore) UpdateUserName(ctx context.Context, id uuid.UUID, name string) (*model.Peer, error) {
	return nil, errors.New("not used")
}

func (s *peerStore) UpdateHandle(ctx context.Context, id uuid.UUID, handle string) (*model.Peer, error) {
	return nil, errors.New("not used")
}

// ---- Harness ----

const (
	cookieName        = "session"
	postLoginRedirect = "http://localhost:3000/"
)

type authEnv struct {
	mux      *http.ServeMux
	provider *fakeProvider
	sessions *fakeSessions
	store    *peerStore
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	e := &authEnv{
		provider: &fakeProvider{identities: map[string]handler.Identity{}},
		sessions: &fakeSessions{issued: map[string]uuid.UUID{}},
		store:    &peerStore{peers: map[uuid.UUID]model.Peer{}},
	}
	h := handler.NewAuthHandler(handler.AuthConfig{
		Providers:         map[string]handler.OAuthProvider{"google": e.provider},
		Sessions:          e.sessions,
		Peers:             service.New(e.store).Peers,
		CookieName:        cookieName,
		PostLoginRedirect: postLoginRedirect,
	})
	// Same patterns as router.registerAuthRoutes, so r.PathValue works.
	e.mux = http.NewServeMux()
	e.mux.HandleFunc("GET /auth/{provider}/login", h.Login)
	e.mux.HandleFunc("GET /auth/{provider}/callback", h.Callback)
	e.mux.HandleFunc("POST /auth/logout", h.Logout)
	return e
}

func (e *authEnv) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// login runs /auth/google/login and returns the state cookie it set.
func (e *authEnv) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := e.do(httptest.NewRequest(http.MethodGet, "/auth/google/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302", rec.Code)
	}
	c := findCookie(rec, "oauth_state")
	if c == nil || c.Value == "" {
		t.Fatal("login did not set the oauth_state cookie")
	}
	return c
}

// callback calls /auth/google/callback with the given query and state cookie.
func (e *authEnv) callback(query url.Values, state *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+query.Encode(), nil)
	if state != nil {
		req.AddCookie(state)
	}
	return e.do(req)
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ---- Login ----

func TestLoginRedirectsToProviderWithState(t *testing.T) {
	e := newAuthEnv(t)
	rec := e.do(httptest.NewRequest(http.MethodGet, "/auth/google/login", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	state := findCookie(rec, "oauth_state")
	if state == nil {
		t.Fatal("no oauth_state cookie")
	}
	if !state.HttpOnly || state.Path != "/auth" || state.MaxAge <= 0 {
		t.Errorf("state cookie = %+v, want HttpOnly, Path=/auth, positive MaxAge", state)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Host != "provider.test" {
		t.Errorf("redirected to %q, want the provider", loc)
	}
	if loc.Query().Get("state") != state.Value {
		t.Error("state in the provider URL does not match the cookie")
	}

	// Each login gets a fresh state.
	if again := e.login(t); again.Value == state.Value {
		t.Error("two logins produced the same state")
	}
}

func TestLoginUnknownProvider(t *testing.T) {
	e := newAuthEnv(t)
	rec := e.do(httptest.NewRequest(http.MethodGet, "/auth/github/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ---- Callback ----

func TestCallbackSignsUpThenSignsIn(t *testing.T) {
	e := newAuthEnv(t)
	e.provider.identities["code-1"] = handler.Identity{Subject: "google-sub-1", Name: "Ada Lovelace"}
	e.provider.identities["code-2"] = handler.Identity{Subject: "google-sub-1", Name: "Renamed At Google"}

	state := e.login(t)
	rec := e.callback(url.Values{"state": {state.Value}, "code": {"code-1"}}, state)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != postLoginRedirect {
		t.Fatalf("got %d to %q, want 302 to %q", rec.Code, rec.Header().Get("Location"), postLoginRedirect)
	}
	session := findCookie(rec, cookieName)
	if session == nil || session.Value == "" {
		t.Fatal("no session cookie")
	}
	if !session.HttpOnly || session.Path != "/" || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie = %+v, want HttpOnly, Path=/, SameSite=Lax", session)
	}
	if c := findCookie(rec, "oauth_state"); c == nil || c.MaxAge >= 0 {
		t.Error("callback did not clear the one-time state cookie")
	}

	peerID, ok := e.sessions.issued[session.Value]
	if !ok {
		t.Fatal("session cookie is not a token issued by the session manager")
	}
	peer, err := e.store.Get(context.Background(), peerID)
	if err != nil {
		t.Fatalf("peer not created: %v", err)
	}
	if peer.UserName != "Ada Lovelace" || peer.Handle != "ada.lovelace" || peer.AuthProvider != "google" || peer.AuthSubject != "google-sub-1" {
		t.Errorf("peer = %+v", peer)
	}

	// Signing in again with the same Google account reuses the peer and keeps its name.
	state = e.login(t)
	rec = e.callback(url.Values{"state": {state.Value}, "code": {"code-2"}}, state)
	second := findCookie(rec, cookieName)
	if second == nil {
		t.Fatalf("second sign-in: status %d, no session cookie", rec.Code)
	}
	if e.sessions.issued[second.Value] != peerID {
		t.Error("second sign-in created a different peer")
	}
	if len(e.store.peers) != 1 {
		t.Errorf("%d peers exist, want 1", len(e.store.peers))
	}
	if p, _ := e.store.Get(context.Background(), peerID); p.UserName != "Ada Lovelace" {
		t.Errorf("user name changed to %q on sign-in", p.UserName)
	}
}

func TestCallbackDisplayNameFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		identity handler.Identity
		want     string
	}{
		{"provider name", handler.Identity{Subject: "s1", Name: "  Grace  ", Email: "g@x.com"}, "Grace"},
		{"email local part", handler.Identity{Subject: "s2", Email: "hopper@navy.mil"}, "hopper"},
		{"placeholder", handler.Identity{Subject: "s3"}, "user"},
		{"truncated", handler.Identity{Subject: "s4", Name: strings.Repeat("x", 100)}, strings.Repeat("x", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			e.provider.identities["c"] = tt.identity
			state := e.login(t)
			rec := e.callback(url.Values{"state": {state.Value}, "code": {"c"}}, state)

			session := findCookie(rec, cookieName)
			if session == nil {
				t.Fatalf("status %d, no session cookie", rec.Code)
			}
			p, err := e.store.Get(context.Background(), e.sessions.issued[session.Value])
			if err != nil {
				t.Fatal(err)
			}
			if p.UserName != tt.want {
				t.Errorf("user name = %q, want %q", p.UserName, tt.want)
			}
		})
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	tests := []struct {
		name   string
		query  func(state string) url.Values
		cookie bool
	}{
		{"no state cookie", func(s string) url.Values { return url.Values{"state": {s}, "code": {"c"}} }, false},
		{"no state param", func(s string) url.Values { return url.Values{"code": {"c"}} }, true},
		{"mismatched state", func(s string) url.Values { return url.Values{"state": {s + "x"}, "code": {"c"}} }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			state := e.login(t)
			var cookie *http.Cookie
			if tt.cookie {
				cookie = state
			}
			rec := e.callback(tt.query(state.Value), cookie)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if len(e.provider.exchanged) != 0 {
				t.Error("code was exchanged despite a bad state")
			}
			if findCookie(rec, cookieName) != nil {
				t.Error("session cookie set despite a bad state")
			}
		})
	}
}

func TestCallbackUserDeclined(t *testing.T) {
	e := newAuthEnv(t)
	state := e.login(t)
	rec := e.callback(url.Values{"state": {state.Value}, "error": {"access_denied"}}, state)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), postLoginRedirect+"?auth_error=access_denied"; got != want {
		t.Errorf("redirect = %q, want %q", got, want)
	}
	if len(e.provider.exchanged) != 0 || findCookie(rec, cookieName) != nil {
		t.Error("declined sign-in should not exchange a code or start a session")
	}
}

func TestCallbackMissingCode(t *testing.T) {
	e := newAuthEnv(t)
	state := e.login(t)
	rec := e.callback(url.Values{"state": {state.Value}}, state)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCallbackExchangeFailure(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		identity handler.Identity
	}{
		{"provider error", errors.New("invalid_grant"), handler.Identity{}},
		{"identity without subject", nil, handler.Identity{Name: "No Subject"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			e.provider.err = tt.err
			e.provider.identities["c"] = tt.identity
			state := e.login(t)
			rec := e.callback(url.Values{"state": {state.Value}, "code": {"c"}}, state)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", rec.Code)
			}
			if findCookie(rec, cookieName) != nil || len(e.store.peers) != 0 {
				t.Error("failed exchange should not create a peer or a session")
			}
		})
	}
}

// ---- Logout ----

func TestLogout(t *testing.T) {
	tests := []struct {
		name      string
		revokeErr error
		wantCode  int
		wantClear bool
	}{
		{"revoked", nil, http.StatusNoContent, true},
		{"already expired", middleware.ErrInvalidToken, http.StatusNoContent, true},
		{"store unavailable", errors.New("db down"), http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			e.sessions.revokeErr = tt.revokeErr

			req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: "tok-abc"})
			rec := e.do(req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if len(e.sessions.revoked) != 1 || e.sessions.revoked[0] != "tok-abc" {
				t.Errorf("revoked = %v, want [tok-abc]", e.sessions.revoked)
			}
			c := findCookie(rec, cookieName)
			if cleared := c != nil && c.MaxAge < 0; cleared != tt.wantClear {
				t.Errorf("session cookie cleared = %v, want %v", cleared, tt.wantClear)
			}
		})
	}
}
