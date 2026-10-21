package router

import "net/http"

// ---- Auth (third-party OAuth) ----

// AuthHandler implements sign up, login and logout.
type AuthHandler interface {
	// Login redirects the user to the OAuth provider named by {provider}.
	Login(w http.ResponseWriter, r *http.Request)
	// Callback completes the OAuth flow. First sign-in creates the peer
	// (sign up); later sign-ins reuse it. Starts the session.
	Callback(w http.ResponseWriter, r *http.Request)
	// Logout ends the caller's session.
	Logout(w http.ResponseWriter, r *http.Request)
}

func registerAuthRoutes(public, authed group, h AuthHandler) {
	public.handle("GET /auth/{provider}/login", h.Login)
	public.handle("GET /auth/{provider}/callback", h.Callback)
	authed.handle("POST /auth/logout", h.Logout)
}

// ---- Peers ----
//
// There is no POST: peers are created by the OAuth callback on first sign-in.
// There is no DELETE yet: room_events.actor_id and members.peer_id reference
// peers, so account deletion needs a decision on how the audit log keeps
// pointing at a removed peer.

type PeerHandler interface {
	GetMe(w http.ResponseWriter, r *http.Request)
	UpdateMe(w http.ResponseWriter, r *http.Request) // e.g. change user_name
	GetByID(w http.ResponseWriter, r *http.Request)  // {peerId}; used to resolve display names
}

func registerPeerRoutes(api group, h PeerHandler) {
	api.handle("GET /peers/me", h.GetMe) // literal beats {peerId} in ServeMux precedence
	api.handle("PATCH /peers/me", h.UpdateMe)
	api.handle("GET /peers/{peerId}", h.GetByID)
}
