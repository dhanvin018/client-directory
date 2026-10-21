package handler

import (
	"log/slog"
	"net/http"

	"github.com/dhanvin018/client-directory/internal/service"
)

type PeerHandler struct {
	base
	svc *service.PeerService
}

func NewPeerHandler(svc *service.PeerService, log *slog.Logger) *PeerHandler {
	return &PeerHandler{base: newBase(log), svc: svc}
}

// GetMe returns the caller's own peer.
func (h *PeerHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	peer, err := h.svc.Get(r.Context(), actor)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, peer)
}

// updateMeRequest uses pointers so an omitted field means "leave unchanged".
type updateMeRequest struct {
	UserName *string `json:"userName"`
	Handle   *string `json:"handle"`
}

// UpdateMe changes the caller's display name and/or handle. A taken handle
// is 409.
func (h *PeerHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	peer, err := h.svc.UpdateProfile(r.Context(), actor, service.UpdateProfileInput{
		UserName: req.UserName,
		Handle:   req.Handle,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, peer)
}

// GetByID resolves a peer ID to a display name for any authenticated caller.
func (h *PeerHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := actorID(w, r); !ok {
		return
	}
	peerID, ok := pathUUID(w, r, "peerId")
	if !ok {
		return
	}
	peer, err := h.svc.Get(r.Context(), peerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, peer)
}
