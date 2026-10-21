package handler

import (
	"log/slog"
	"net/http"

	"github.com/dhanvin018/client-directory/internal/service"

	"github.com/dhanvin018/client-directory/internal/model"
)

type MemberHandler struct {
	base
	svc *service.MemberService
}

func NewMemberHandler(svc *service.MemberService, log *slog.Logger) *MemberHandler {
	return &MemberHandler{base: newBase(log), svc: svc}
}

func (h *MemberHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	members, err := h.svc.List(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(members))
}

func (h *MemberHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	member, err := h.svc.Get(r.Context(), actor, roomID, peerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

// Join adds the caller to a public room: 201 when they joined, 200 when they
// were already a member.
func (h *MemberHandler) Join(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	member, created, err := h.svc.Join(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, member)
}

// Remove removes a member. The caller may remove themselves (leave the room).
func (h *MemberHandler) Remove(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	if err := h.svc.Remove(r.Context(), actor, roomID, peerID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}

type updateRoomRoleRequest struct {
	RoomRole model.RoomRoles `json:"roomRole"`
}

func (h *MemberHandler) UpdateRoomRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	var req updateRoomRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	member, err := h.svc.UpdateRoomRole(r.Context(), actor, roomID, peerID, req.RoomRole)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

type updateDocRoleRequest struct {
	DocRole model.DocRoles `json:"docRole"`
}

func (h *MemberHandler) UpdateDocRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	docID, ok := pathUUID(w, r, "docId")
	if !ok {
		return
	}
	var req updateDocRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	member, err := h.svc.UpdateDocRole(r.Context(), actor, roomID, docID, peerID, req.DocRole)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (h *MemberHandler) ListBans(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	bans, err := h.svc.ListBans(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(bans))
}

// Ban removes the peer from the room (if a member) and keeps them out.
func (h *MemberHandler) Ban(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	if err := h.svc.Ban(r.Context(), actor, roomID, peerID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}

func (h *MemberHandler) Unban(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	if err := h.svc.Unban(r.Context(), actor, roomID, peerID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}
