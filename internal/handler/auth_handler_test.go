package handler_test

import (
	"bytes"
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

// fakeTokens signs nothing: a token is an opaque string remembered with the
// peer and session it was issued for. It is also the middleware's verifier.
type fakeTokens struct {
	mu     sync.Mutex
	issued map[string]issuedToken
}

type issuedToken struct{ peer, session uuid.UUID }

func (f *fakeTokens) Issue(ctx context.Context, peerID, sessionID uuid.UUID) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := "tok-" + uuid.NewString()
	f.issued[token] = issuedToken{peerID, sessionID}
	return token, time.Now().Add(15 * time.Minute), nil
}

func (f *fakeTokens) lookup(token string) (issuedToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.issued[token]
	if !ok {
		return issuedToken{}, middleware.ErrInvalidToken
	}
	return t, nil
}

func (f *fakeTokens) Verify(ctx context.Context, token string) (uuid.UUID, error) {
	t, err := f.lookup(token)
	return t.peer, err
}

func (f *fakeTokens) SessionID(ctx context.Context, token string) (uuid.UUID, error) {
	t, err := f.lookup(token)
	return t.session, err
}

func (f *fakeTokens) peer(token string) uuid.UUID {
	t, _ := f.lookup(token)
	return t.peer
}

// peerStore is the minimum service.Store the PeerService needs; the other
// repositories are never touched by the auth flow.
type peerStore struct {
	mu     sync.Mutex
	peers  map[uuid.UUID]model.Peer
	logins map[string]service.DesktopLogin
	codes  map[string]service.DesktopAuthCode

	sessions   map[uuid.UUID]service.Session
	sessionErr error // simulates the sessions table being unreachable
}

func (s *peerStore) Peers() service.PeerRepository         { return s }
func (s *peerStore) Rooms() service.RoomRepository         { return nil }
func (s *peerStore) Documents() service.DocumentRepository { return nil }
func (s *peerStore) Members() service.MemberRepository     { return nil }
func (s *peerStore) Events() service.EventRepository       { return nil }
func (s *peerStore) Invites() service.InviteRepository     { return nil }
func (s *peerStore) Bans() service.BanRepository           { return nil }
func (s *peerStore) DesktopLogins() service.DesktopLoginRepository {
	return desktopStore{s}
}
func (s *peerStore) Sessions() service.SessionRepository { return sessionStore{s} }
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

// desktopStore keeps desktop sign-ins in the same fake.
type desktopStore struct{ s *peerStore }

func (d desktopStore) SaveLogin(ctx context.Context, l service.DesktopLogin) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	d.s.logins[l.State] = l
	return nil
}

func (d desktopStore) TakeLogin(ctx context.Context, state string, now time.Time) (*service.DesktopLogin, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	l, ok := d.s.logins[state]
	if !ok || !l.ExpiresAt.After(now) {
		return nil, service.ErrNotFound
	}
	delete(d.s.logins, state)
	return &l, nil
}

func (d desktopStore) SaveCode(ctx context.Context, c service.DesktopAuthCode) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	d.s.codes[string(c.CodeHash)] = c
	return nil
}

func (d desktopStore) TakeCode(ctx context.Context, hash []byte, now time.Time) (*service.DesktopAuthCode, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	c, ok := d.s.codes[string(hash)]
	if !ok || !c.ExpiresAt.After(now) {
		return nil, service.ErrNotFound
	}
	delete(d.s.codes, string(hash))
	return &c, nil
}

func (d desktopStore) DeleteExpired(ctx context.Context, now time.Time) error { return nil }

// sessionStore keeps sessions in the same fake. It does not track previous
// hashes; reuse detection is covered by the service tests.
type sessionStore struct{ s *peerStore }

func (d sessionStore) Create(ctx context.Context, sess service.Session) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.s.sessionErr != nil {
		return d.s.sessionErr
	}
	d.s.sessions[sess.SessionID] = sess
	return nil
}

func (d sessionStore) Rotate(ctx context.Context, oldHash, newHash []byte, expiresAt, now time.Time) (*service.Session, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.s.sessionErr != nil {
		return nil, d.s.sessionErr
	}
	for id, sess := range d.s.sessions {
		if bytes.Equal(sess.RefreshHash, oldHash) && sess.ExpiresAt.After(now) {
			sess.RefreshHash, sess.ExpiresAt = newHash, expiresAt
			d.s.sessions[id] = sess
			return &sess, nil
		}
	}
	return nil, service.ErrNotFound
}

func (d sessionStore) DeleteByHash(ctx context.Context, hash []byte) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.s.sessionErr != nil {
		return d.s.sessionErr
	}
	for id, sess := range d.s.sessions {
		if bytes.Equal(sess.RefreshHash, hash) {
			delete(d.s.sessions, id)
			return nil
		}
	}
	return service.ErrNotFound
}

func (d sessionStore) Delete(ctx context.Context, id uuid.UUID) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.s.sessionErr != nil {
		return d.s.sessionErr
	}
	if _, ok := d.s.sessions[id]; !ok {
		return service.ErrNotFound
	}
	delete(d.s.sessions, id)
	return nil
}

func (d sessionStore) DeleteForPeer(ctx context.Context, peerID uuid.UUID) error {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.s.sessionErr != nil {
		return d.s.sessionErr
	}
	for id, sess := range d.s.sessions {
		if sess.PeerID == peerID {
			delete(d.s.sessions, id)
		}
	}
	return nil
}

func (d sessionStore) DeleteExpired(ctx context.Context, now time.Time) error { return nil }

// ---- Harness ----

const (
	cookieName        = "session"
	refreshCookie     = "refresh"
	postLoginRedirect = "http://localhost:3000/"
)

type authEnv struct {
	mux      *http.ServeMux
	provider *fakeProvider
	tokens   *fakeTokens
	store    *peerStore
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	e := &authEnv{
		provider: &fakeProvider{identities: map[string]handler.Identity{}},
		tokens:   &fakeTokens{issued: map[string]issuedToken{}},
		store: &peerStore{
			peers:    map[uuid.UUID]model.Peer{},
			logins:   map[string]service.DesktopLogin{},
			codes:    map[string]service.DesktopAuthCode{},
			sessions: map[uuid.UUID]service.Session{},
		},
	}
	svc := service.New(e.store)
	h := handler.NewAuthHandler(handler.AuthConfig{
		Providers:         map[string]handler.OAuthProvider{"google": e.provider},
		Tokens:            e.tokens,
		Sessions:          svc.Sessions,
		Peers:             svc.Peers,
		Desktop:           svc.Desktop,
		CookieName:        cookieName,
		PostLoginRedirect: postLoginRedirect,
	})
	// Same patterns as router.registerAuthRoutes, so r.PathValue works.
	e.mux = http.NewServeMux()
	e.mux.HandleFunc("GET /auth/{provider}/login", h.Login)
	e.mux.HandleFunc("GET /auth/{provider}/callback", h.Callback)
	e.mux.HandleFunc("POST /auth/token", h.Token)
	e.mux.HandleFunc("POST /auth/refresh", h.Refresh)
	e.mux.HandleFunc("POST /auth/logout", h.Logout)
	authed := middleware.Authenticate(middleware.AuthConfig{Verifier: e.tokens, CookieName: cookieName})
	e.mux.Handle("POST /auth/logout-all", authed(http.HandlerFunc(h.LogoutAll)))
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

	peerID := e.tokens.peer(session.Value)
	if peerID == uuid.Nil {
		t.Fatal("session cookie is not an issued access token")
	}
	refresh := findCookie(rec, refreshCookie)
	if refresh == nil || refresh.Value == "" || !refresh.HttpOnly || refresh.Path != "/auth" {
		t.Fatalf("refresh cookie = %+v, want HttpOnly with Path=/auth", refresh)
	}
	if len(e.store.sessions) != 1 {
		t.Fatalf("%d sessions stored, want 1", len(e.store.sessions))
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
	if e.tokens.peer(second.Value) != peerID {
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
			p, err := e.store.Get(context.Background(), e.tokens.peer(session.Value))
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

// ---- Refresh and logout ----

// browserSignIn signs a browser in and returns its access and refresh cookies.
func (e *authEnv) browserSignIn(t *testing.T) (access, refresh *http.Cookie) {
	t.Helper()
	e.provider.identities["c"] = handler.Identity{Subject: "sub-1", Name: "Ada"}
	state := e.login(t)
	rec := e.callback(url.Values{"state": {state.Value}, "code": {"c"}}, state)
	access, refresh = findCookie(rec, cookieName), findCookie(rec, refreshCookie)
	if access == nil || refresh == nil {
		t.Fatalf("sign-in: status %d, cookies %v", rec.Code, rec.Result().Cookies())
	}
	return access, refresh
}

func (e *authEnv) post(path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return e.do(req)
}

func TestBrowserRefreshUsesCookies(t *testing.T) {
	e := newAuthEnv(t)
	access, refresh := e.browserSignIn(t)

	rec := e.post("/auth/refresh", "", refresh)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Token") {
		t.Errorf("cookie refresh exposed tokens in the body: %s", rec.Body.String())
	}
	newAccess, newRefresh := findCookie(rec, cookieName), findCookie(rec, refreshCookie)
	if newAccess == nil || newAccess.Value == access.Value || newRefresh == nil || newRefresh.Value == refresh.Value {
		t.Fatal("refresh did not set new access and refresh cookies")
	}
	if e.tokens.peer(newAccess.Value) != e.tokens.peer(access.Value) {
		t.Error("new access token is for another peer")
	}

	// The old refresh token is spent; a failed cookie refresh clears the cookies.
	rec = e.post("/auth/refresh", "", refresh)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("spent refresh token: status %d, want 401", rec.Code)
	}
	if c := findCookie(rec, refreshCookie); c == nil || c.MaxAge >= 0 {
		t.Error("failed refresh did not clear the refresh cookie")
	}
}

func TestRefreshWithoutToken(t *testing.T) {
	e := newAuthEnv(t)
	if rec := e.post("/auth/refresh", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec := e.post("/auth/refresh", `{"refreshToken":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: status = %d, want 401", rec.Code)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	tests := []struct {
		name    string
		cookies func(access, refresh *http.Cookie) []*http.Cookie
	}{
		{"with the refresh cookie", func(a, r *http.Cookie) []*http.Cookie { return []*http.Cookie{a, r} }},
		// The access cookie alone names the session (expired ones too).
		{"with only the access token", func(a, r *http.Cookie) []*http.Cookie { return []*http.Cookie{a} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAuthEnv(t)
			access, refresh := e.browserSignIn(t)

			rec := e.post("/auth/logout", "", tt.cookies(access, refresh)...)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			if len(e.store.sessions) != 0 {
				t.Error("session still stored after logout")
			}
			for _, name := range []string{cookieName, refreshCookie} {
				if c := findCookie(rec, name); c == nil || c.MaxAge >= 0 {
					t.Errorf("cookie %s not cleared", name)
				}
			}
			if rec := e.post("/auth/refresh", "", refresh); rec.Code != http.StatusUnauthorized {
				t.Errorf("refresh after logout: status %d, want 401", rec.Code)
			}
		})
	}
}

func TestLogoutIsIdempotent(t *testing.T) {
	e := newAuthEnv(t)
	for _, body := range []string{"", `{"refreshToken":"unknown"}`} {
		if rec := e.post("/auth/logout", body); rec.Code != http.StatusNoContent {
			t.Errorf("logout with body %q: status %d, want 204", body, rec.Code)
		}
	}
}

func TestLogoutStoreUnavailable(t *testing.T) {
	e := newAuthEnv(t)
	_, refresh := e.browserSignIn(t)
	e.store.sessionErr = errors.New("db down")

	rec := e.post("/auth/logout", "", refresh)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if findCookie(rec, refreshCookie) != nil {
		t.Error("cookies cleared although the session could not be ended")
	}
}

func TestLogoutAll(t *testing.T) {
	e := newAuthEnv(t)
	access, refresh := e.browserSignIn(t)
	_, other := e.browserSignIn(t) // a second device of the same user

	if rec := e.post("/auth/logout-all", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: status %d, want 401", rec.Code)
	}
	if rec := e.post("/auth/logout-all", "", access); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	for _, r := range []*http.Cookie{refresh, other} {
		if rec := e.post("/auth/refresh", "", r); rec.Code != http.StatusUnauthorized {
			t.Errorf("refresh after logout-all: status %d, want 401", rec.Code)
		}
	}
}
