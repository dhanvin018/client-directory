package repository_test

// End-to-end API test: the real router, handlers, session tokens, services and
// Postgres, driven over HTTP the way the client app will use them.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhanvin018/client-directory/internal/auth"
	"github.com/dhanvin018/client-directory/internal/handler"
	"github.com/dhanvin018/client-directory/internal/middleware"
	"github.com/dhanvin018/client-directory/internal/router"
)

type apiEnv struct {
	*env
	srv      *httptest.Server
	sessions *auth.JWTSessionManager
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := newEnv(t)
	sessions := auth.NewJWTSessionManager("api-test-secret-at-least-32-bytes-long", time.Hour)
	h := router.New(router.Config{
		Handlers: router.Handlers{
			Auth: handler.NewAuthHandler(handler.AuthConfig{
				Providers:         map[string]handler.OAuthProvider{"test": nil},
				Sessions:          sessions,
				Peers:             e.svc.Peers,
				CookieName:        "session",
				PostLoginRedirect: "http://app.test/",
			}),
			Peers:     handler.NewPeerHandler(e.svc.Peers, nil),
			Rooms:     handler.NewRoomHandler(e.svc.Rooms, nil),
			Documents: handler.NewDocumentHandler(e.svc.Documents, nil),
			Members:   handler.NewMemberHandler(e.svc.Members, nil),
			Events:    handler.NewEventHandler(e.svc.Events, nil),
			Invites:   handler.NewInviteHandler(e.svc.Invites, "http://app.test/invite/", nil),
		},
		Authenticate: middleware.Authenticate(middleware.AuthConfig{Verifier: sessions, CookieName: "session"}),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiEnv{env: e, srv: srv, sessions: sessions}
}

// user signs a peer up and returns a bearer token for them.
func (a *apiEnv) user(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	id := a.peer(t, name)
	token, _, err := a.sessions.Issue(context.Background(), id)
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
		InviteID, Code, URL, Status string
	}
	expect(t, "invite", a.call(t, alice, "POST", base+"/invites", map[string]any{"inviteePeerId": cands[0].PeerID}, &inv), http.StatusCreated)
	if inv.URL != "http://app.test/invite/"+inv.Code || inv.Status != "PENDING" {
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
