package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type InviteHandler struct {
	base
	svc *service.InviteService

	// linkBase is prepended to an invite's code to build the shareable web
	// link, e.g. "http://localhost:3000/invite/": a page that opens the
	// desktop app (or explains how to get it).
	linkBase string
	// appLinkBase builds the deep link that opens the desktop app directly,
	// e.g. "collabdoc://invite/". Empty means no appUrl in responses.
	appLinkBase string
}

func NewInviteHandler(svc *service.InviteService, linkBase, appLinkBase string, log *slog.Logger) *InviteHandler {
	if linkBase == "" {
		panic("handler: invite link base is required")
	}
	return &InviteHandler{base: newBase(log), svc: svc, linkBase: linkBase, appLinkBase: appLinkBase}
}

// inviteResponse is an invite plus its ready-to-send links.
type inviteResponse struct {
	model.RoomInvite
	URL    string `json:"url"`              // web link, works anywhere
	AppURL string `json:"appUrl,omitempty"` // deep link into the desktop app
}

func (h *InviteHandler) withLink(inv model.RoomInvite) inviteResponse {
	resp := inviteResponse{RoomInvite: inv, URL: h.linkBase + inv.Code}
	if h.appLinkBase != "" {
		resp.AppURL = h.appLinkBase + inv.Code
	}
	return resp
}

func (h *InviteHandler) withLinks(invites []model.RoomInvite) []inviteResponse {
	out := make([]inviteResponse, len(invites))
	for i, inv := range invites {
		out[i] = h.withLink(inv)
	}
	return out
}

// Candidates searches people to invite: GET /rooms/{roomId}/invite-candidates?q=ada.
func (h *InviteHandler) Candidates(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	candidates, err := h.svc.Candidates(r.Context(), actor, roomID, r.URL.Query().Get("q"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(candidates))
}

type createInviteRequest struct {
	InviteePeerID string `json:"inviteePeerId"`
}

// Create returns 201 with a new invite, or 200 with the invitee's existing
// pending invite.
func (h *InviteHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	var req createInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	inviteeID, ok := parseUUID(w, "inviteePeerId", req.InviteePeerID)
	if !ok {
		return
	}
	invite, created, err := h.svc.Create(r.Context(), actor, roomID, inviteeID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, h.withLink(*invite))
}

// ListForRoom returns the room's pending invites.
func (h *InviteHandler) ListForRoom(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	invites, err := h.svc.ListForRoom(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withLinks(invites))
}

func (h *InviteHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	inviteID, ok := pathUUID(w, r, "inviteId")
	if !ok {
		return
	}
	if err := h.svc.Revoke(r.Context(), actor, roomID, inviteID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}

// Inbox returns the caller's pending invites (their notifications).
func (h *InviteHandler) Inbox(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	invites, err := h.svc.Inbox(r.Context(), actor)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withLinks(invites))
}

// pathCode reads {code}. Codes are opaque, so only emptiness is checked here.
func pathCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "invite code is required")
		return "", false
	}
	return code, true
}

// Get previews an invite for its invitee; anyone else gets 404.
func (h *InviteHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	code, ok := pathCode(w, r)
	if !ok {
		return
	}
	invite, err := h.svc.Get(r.Context(), actor, code)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withLink(*invite))
}

type acceptInviteResponse struct {
	Room   *model.Room   `json:"room"`
	Member *model.Member `json:"member"`
}

func (h *InviteHandler) Accept(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	code, ok := pathCode(w, r)
	if !ok {
		return
	}
	room, member, err := h.svc.Accept(r.Context(), actor, code)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, acceptInviteResponse{Room: room, Member: member})
}

func (h *InviteHandler) Decline(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	code, ok := pathCode(w, r)
	if !ok {
		return
	}
	if err := h.svc.Decline(r.Context(), actor, code); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}
