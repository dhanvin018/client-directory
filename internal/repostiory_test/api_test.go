package repository_test

// End-to-end API test: the real router, handlers, session tokens, services and
// Postgres, driven over HTTP the way the client app will use them.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/auth"
	"github.com/dhanvin018/client-directory/internal/handler"
	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/router"
	"github.com/dhanvin018/client-directory/internal/service"
)

// apiProvider stands in for Google: the code is the user's subject.
type apiProvider struct{}

func (apiProvider) AuthCodeURL(state string) string {
	return "https://provider.test/authorize?state=" + url.QueryEscape(state)
}

func (apiProvider) Exchange(ctx context.Context, code string) (handler.Identity, error) {
	return handler.Identity{Subject: code, Name: "Desk Top"}, nil
}

type apiEnv struct {
	*env
	srv    *httptest.Server
	tokens *auth.AccessTokens
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	return startAPI(t, newEnv(t))
}

// startAPI starts a server over e's database. Calling it again on the same
// database is a restart: nothing but the database carries over.
func startAPI(t *testing.T, e *env) *apiEnv {
	t.Helper()
	e = &env{db: e.db, store: e.store, svc: service.New(e.store)}
	tokens := auth.NewAccessTokens("api-test-secret-at-least-32-bytes-long", 15*time.Minute)
	h := router.New(router.Config{
		Handlers: router.Handlers{
			Auth: handler.NewAuthHandler(handler.AuthConfig{
				Providers:         map[string]handler.OAuthProvider{"test": apiProvider{}},
				Tokens:            tokens,
				Sessions:          e.svc.Sessions,
				Peers:             e.svc.Peers,
				Desktop:           e.svc.Desktop,
				CookieName:        "session",
				PostLoginRedirect: "http://app.test/",
			}),
			Peers:     handler.NewPeerHandler(e.svc.Peers, nil),
			Rooms:     handler.NewRoomHandler(e.svc.Rooms, nil),
			Documents: handler.NewDocumentHandler(e.svc.Documents, nil),
			Members:   handler.NewMemberHandler(e.svc.Members, nil),
			Events:    handler.NewEventHandler(e.svc.Events, nil),
			Invites:   handler.NewInviteHandler(e.svc.Invites, "http://app.test/invite/", "collabdoc://invite/", nil),
		},
		Authenticate: middleware.Authenticate(middleware.AuthConfig{Verifier: tokens, CookieName: "session"}),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiEnv{env: e, srv: srv, tokens: tokens}
}

// user signs a peer up and returns a bearer token for them.
func (a *apiEnv) user(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	id := a.peer(t, name)
	sess, err := a.svc.Sessions.Start(context.Background(), id)
	mustNoErr(t, "start session", err)
	token, _, err := a.tokens.Issue(context.Background(), id, sess.SessionID)
	mustNoErr(t, "issue token", err)
	return id, token
}

// call sends a request and decodes a JSON response into out (if non-nil).
func (a *apiEnv) call(t *testing.T, token, method, path string, body any, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	mustNoErr(t, "new request", err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	mustNoErr(t, method+" "+path, err)
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		mustNoErr(t, "decode "+path, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}

func expect(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

func TestAPIPrivateRoomInviteFlow(t *testing.T) {
	a := newAPIEnv(t)
	_, alice := a.user(t, "alice")
	bobID, bob := a.user(t, "bob")
	_, eve := a.user(t, "eve")

	expect(t, "no token", a.call(t, "", "GET", "/api/v1/invites", nil, nil), http.StatusUnauthorized)

	var room struct{ RoomID string }
	expect(t, "create room", a.call(t, alice, "POST", "/api/v1/rooms", map[string]any{"name": "Secret"}, &room), http.StatusCreated)
	base := "/api/v1/rooms/" + room.RoomID

	// The old direct-add endpoint is gone.
	expect(t, "direct add", a.call(t, alice, "POST", base+"/members", map[string]any{"peerId": bobID}, nil), http.StatusMethodNotAllowed)

	var cands []struct {
		PeerID, Handle, Status string
	}
	expect(t, "search", a.call(t, alice, "GET", base+"/invite-candidates?q=bo", nil, &cands), http.StatusOK)
	if len(cands) != 1 || cands[0].Handle != "bob" || cands[0].Status != "AVAILABLE" {
		t.Fatalf("candidates = %+v", cands)
	}
	expect(t, "search too short", a.call(t, alice, "GET", base+"/invite-candidates?q=b", nil, nil), http.StatusBadRequest)

	var inv struct {
		InviteID, Code, URL, AppURL, Status string
	}
	expect(t, "invite", a.call(t, alice, "POST", base+"/invites", map[string]any{"inviteePeerId": cands[0].PeerID}, &inv), http.StatusCreated)
	if inv.URL != "http://app.test/invite/"+inv.Code || inv.AppURL != "collabdoc://invite/"+inv.Code || inv.Status != "PENDING" {
		t.Fatalf("invite = %+v", inv)
	}
	expect(t, "re-invite", a.call(t, alice, "POST", base+"/invites", map[string]any{"inviteePeerId": cands[0].PeerID}, nil), http.StatusOK)

	var inbox []struct {
		Code     string
		RoomName string
		Inviter  struct{ Handle string }
	}
	expect(t, "inbox", a.call(t, bob, "GET", "/api/v1/invites", nil, &inbox), http.StatusOK)
	if len(inbox) != 1 || inbox[0].RoomName != "Secret" || inbox[0].Inviter.Handle != "alice" {
		t.Fatalf("inbox = %+v", inbox)
	}

	expect(t, "forwarded link", a.call(t, eve, "GET", "/api/v1/invites/"+inv.Code, nil, nil), http.StatusNotFound)
	expect(t, "outsider reads room", a.call(t, eve, "GET", base, nil, nil), http.StatusNotFound)
	expect(t, "preview", a.call(t, bob, "GET", "/api/v1/invites/"+inv.Code, nil, nil), http.StatusOK)

	var accepted struct {
		Room   struct{ Name string }
		Member struct{ RoomRole string }
	}
	expect(t, "accept", a.call(t, bob, "POST", "/api/v1/invites/"+inv.Code+"/accept", nil, &accepted), http.StatusOK)
	if accepted.Room.Name != "Secret" || accepted.Member.RoomRole != "PARTICIPANT" {
		t.Fatalf("accept = %+v", accepted)
	}
	expect(t, "accept again", a.call(t, bob, "POST", "/api/v1/invites/"+inv.Code+"/accept", nil, nil), http.StatusConflict)
	expect(t, "bob reads room", a.call(t, bob, "GET", base, nil, nil), http.StatusOK)

	// Bob (a participant) can invite too, and decline/revoke work over HTTP.
	_, carol := a.user(t, "carol")
	expect(t, "search carol", a.call(t, bob, "GET", base+"/invite-candidates?q=car", nil, &cands), http.StatusOK)
	expect(t, "bob invites", a.call(t, bob, "POST", base+"/invites", map[string]any{"inviteePeerId": cands[0].PeerID}, &inv), http.StatusCreated)
	expect(t, "carol declines", a.call(t, carol, "POST", "/api/v1/invites/"+inv.Code+"/decline", nil, nil), http.StatusNoContent)
	expect(t, "re-invite carol", a.call(t, bob, "POST", base+"/invites", map[string]any{"inviteePeerId": cands[0].PeerID}, &inv), http.StatusCreated)
	expect(t, "eve revokes", a.call(t, eve, "DELETE", base+"/invites/"+inv.InviteID, nil, nil), http.StatusNotFound)
	expect(t, "alice revokes", a.call(t, alice, "DELETE", base+"/invites/"+inv.InviteID, nil, nil), http.StatusNoContent)
	var pending []any
	expect(t, "room invites", a.call(t, alice, "GET", base+"/invites", nil, &pending), http.StatusOK)
	if len(pending) != 0 {
		t.Fatalf("%d invites still pending", len(pending))
	}
}

func TestAPIPublicRoomJoinAndBan(t *testing.T) {
	a := newAPIEnv(t)
	_, alice := a.user(t, "alice")
	bobID, bob := a.user(t, "bob")

	var room struct{ RoomID string }
	expect(t, "create", a.call(t, alice, "POST", "/api/v1/rooms", map[string]any{"name": "Town Square", "visibility": "PUBLIC"}, &room), http.StatusCreated)
	base := "/api/v1/rooms/" + room.RoomID

	var found []struct {
		RoomID      string
		MemberCount int
	}
	// "discover" must route to the search, not be parsed as a {roomId}.
	expect(t, "discover", a.call(t, bob, "GET", "/api/v1/rooms/discover?q=town", nil, &found), http.StatusOK)
	if len(found) != 1 || found[0].RoomID != room.RoomID || found[0].MemberCount != 1 {
		t.Fatalf("discover = %+v", found)
	}
	expect(t, "preview", a.call(t, bob, "GET", base+"/preview", nil, nil), http.StatusOK)
	expect(t, "invite in public room", a.call(t, alice, "POST", base+"/invites", map[string]any{"inviteePeerId": bobID}, nil), http.StatusConflict)

	expect(t, "join", a.call(t, bob, "POST", base+"/join", nil, nil), http.StatusCreated)
	expect(t, "join again", a.call(t, bob, "POST", base+"/join", nil, nil), http.StatusOK)

	expect(t, "ban yourself", a.call(t, bob, "PUT", base+"/bans/"+bobID.String(), nil, nil), http.StatusBadRequest)
	expect(t, "ban", a.call(t, alice, "PUT", base+"/bans/"+bobID.String(), nil, nil), http.StatusNoContent)
	expect(t, "banned join", a.call(t, bob, "POST", base+"/join", nil, nil), http.StatusForbidden)
	var bans []struct{ Peer struct{ Handle string } }
	expect(t, "list bans", a.call(t, alice, "GET", base+"/bans", nil, &bans), http.StatusOK)
	if len(bans) != 1 || bans[0].Peer.Handle != "bob" {
		t.Fatalf("bans = %+v", bans)
	}
	expect(t, "unban", a.call(t, alice, "DELETE", base+"/bans/"+bobID.String(), nil, nil), http.StatusNoContent)
	expect(t, "rejoin", a.call(t, bob, "POST", base+"/join", nil, nil), http.StatusCreated)
}

func TestAPIProfileHandle(t *testing.T) {
	a := newAPIEnv(t)
	_, alice := a.user(t, "alice")
	_, bob := a.user(t, "bob")

	var me struct{ Handle, UserName string }
	expect(t, "me", a.call(t, alice, "GET", "/api/v1/peers/me", nil, &me), http.StatusOK)
	if me.Handle != "alice" {
		t.Fatalf("me = %+v", me)
	}
	expect(t, "set handle", a.call(t, alice, "PATCH", "/api/v1/peers/me", map[string]any{"handle": "@Ada"}, &me), http.StatusOK)
	if me.Handle != "ada" || me.UserName != "alice" {
		t.Fatalf("me = %+v", me)
	}
	expect(t, "taken", a.call(t, bob, "PATCH", "/api/v1/peers/me", map[string]any{"handle": "ada"}, nil), http.StatusConflict)
	expect(t, "invalid", a.call(t, bob, "PATCH", "/api/v1/peers/me", map[string]any{"handle": "no spaces"}, nil), http.StatusBadRequest)
	expect(t, "empty", a.call(t, bob, "PATCH", "/api/v1/peers/me", map[string]any{}, nil), http.StatusBadRequest)
	expect(t, "unknown field", a.call(t, bob, "PATCH", "/api/v1/peers/me", map[string]any{"nick": "x"}, nil), http.StatusBadRequest)

	var other struct{ Handle string }
	expect(t, "lookup", a.call(t, bob, "GET", "/api/v1/peers/me", nil, &other), http.StatusOK)
	if !strings.HasPrefix(other.Handle, "bob") {
		t.Fatalf("bob's handle = %q", other.Handle)
	}
}

// A desktop app signs in through the system browser and ends up with a bearer
// token, never a cookie. The browser is simulated with a client that does not
// follow redirects, so each hop can be checked.
func TestAPIDesktopSignIn(t *testing.T) {
	a := newAPIEnv(t)
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	verifier := strings.Repeat("0123456789", 5)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	loopback := "http://127.0.0.1:53682/callback"

	// 1. The app opens the system browser at the login URL.
	q := url.Values{"redirect_uri": {loopback}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"app-state"}}
	resp, err := browser.Get(a.srv.URL + "/auth/test/login?" + q.Encode())
	mustNoErr(t, "login", err)
	resp.Body.Close()
	expect(t, "login", resp.StatusCode, http.StatusFound)
	providerURL, _ := url.Parse(resp.Header.Get("Location"))
	var stateCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("no oauth_state cookie")
	}

	// 2. The provider sends the browser back to the directory's callback.
	cb, _ := http.NewRequest("GET", a.srv.URL+"/auth/test/callback?"+url.Values{
		"state": {providerURL.Query().Get("state")}, "code": {"desktop-user"},
	}.Encode(), nil)
	cb.AddCookie(stateCookie)
	resp, err = browser.Do(cb)
	mustNoErr(t, "callback", err)
	resp.Body.Close()
	expect(t, "callback", resp.StatusCode, http.StatusFound)
	appURL, _ := url.Parse(resp.Header.Get("Location"))
	if appURL.Host != "127.0.0.1:53682" || appURL.Query().Get("state") != "app-state" || appURL.Query().Get("code") == "" {
		t.Fatalf("callback redirected to %q, want the app's loopback URI with a code", resp.Header.Get("Location"))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			t.Fatal("desktop sign-in set a browser session cookie")
		}
	}

	// 3. The app's loopback listener got the code; it trades it for a token.
	var tok struct {
		AccessToken, TokenType, RefreshToken string
		Peer                                 struct{ Handle string }
	}
	body := map[string]any{"code": appURL.Query().Get("code"), "codeVerifier": verifier, "redirectUri": loopback}
	expect(t, "token", a.call(t, "", "POST", "/auth/token", body, &tok), http.StatusOK)
	if tok.TokenType != "Bearer" || tok.Peer.Handle != "desk.top" {
		t.Fatalf("token = %+v", tok)
	}
	expect(t, "token reuse", a.call(t, "", "POST", "/auth/token", body, nil), http.StatusBadRequest)

	// 4. The token works as a bearer token on the API.
	var me struct{ Handle string }
	expect(t, "me", a.call(t, tok.AccessToken, "GET", "/api/v1/peers/me", nil, &me), http.StatusOK)
	if me.Handle != "desk.top" {
		t.Fatalf("me = %+v", me)
	}
	if a.count(t, "desktop_logins")+a.count(t, "desktop_auth_codes") != 0 {
		t.Fatal("used desktop login rows were not deleted")
	}
	if a.count(t, "sessions") != 1 {
		t.Fatalf("%d sessions stored, want 1", a.count(t, "sessions"))
	}

	// 5. Later, the app refreshes with the refresh token from the keychain.
	var next struct{ AccessToken, RefreshToken string }
	refresh := map[string]any{"refreshToken": tok.RefreshToken}
	expect(t, "refresh", a.call(t, "", "POST", "/auth/refresh", refresh, &next), http.StatusOK)
	expect(t, "me after refresh", a.call(t, next.AccessToken, "GET", "/api/v1/peers/me", nil, nil), http.StatusOK)
	// Presenting the spent token again looks like theft: the session ends.
	expect(t, "spent refresh token", a.call(t, "", "POST", "/auth/refresh", refresh, nil), http.StatusUnauthorized)
	expect(t, "refresh after reuse", a.call(t, "", "POST", "/auth/refresh",
		map[string]any{"refreshToken": next.RefreshToken}, nil), http.StatusUnauthorized)
	if a.count(t, "sessions") != 0 {
		t.Fatal("reusing a refresh token did not end the session")
	}
}

// Logout is stored in Postgres, so it survives a restart: the old in-memory
// revocation list did not.
func TestAPILogoutSurvivesRestart(t *testing.T) {
	a := newAPIEnv(t)
	id := a.peer(t, "alice")
	sess, err := a.svc.Sessions.Start(context.Background(), id)
	mustNoErr(t, "start session", err)
	access, _, err := a.tokens.Issue(context.Background(), id, sess.SessionID)
	mustNoErr(t, "issue token", err)
	other, err := a.svc.Sessions.Start(context.Background(), id) // a second device
	mustNoErr(t, "start session", err)

	expect(t, "logout", a.call(t, access, "POST", "/auth/logout", nil, nil), http.StatusNoContent)

	restarted := startAPI(t, a.env)
	expect(t, "refresh after restart", restarted.call(t, "", "POST", "/auth/refresh",
		map[string]any{"refreshToken": sess.RefreshToken}, nil), http.StatusUnauthorized)
	// Only that device was logged out.
	expect(t, "other device", restarted.call(t, "", "POST", "/auth/refresh",
		map[string]any{"refreshToken": other.RefreshToken}, nil), http.StatusOK)
}
