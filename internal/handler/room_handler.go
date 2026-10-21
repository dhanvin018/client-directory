package handler

import (
	"log/slog"
	"net/http"

	"github.com/dhanvin018/client-directory/internal/model"
	"github.com/dhanvin018/client-directory/internal/service"
)

type RoomHandler struct {
	base
	svc *service.RoomService
}

func NewRoomHandler(svc *service.RoomService, log *slog.Logger) *RoomHandler {
	return &RoomHandler{base: newBase(log), svc: svc}
}

type createRoomRequest struct {
	Name           string           `json:"name"`
	Visibility     model.Visibility `json:"visibility"`     // optional, defaults to PRIVATE
	DefaultDocRole model.DocRoles   `json:"defaultDocRole"` // optional, defaults to VIEWER
}

func (h *RoomHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	var req createRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	room, err := h.svc.Create(r.Context(), actor, service.CreateRoomInput{
		Name:           req.Name,
		Visibility:     req.Visibility,
		DefaultDocRole: req.DefaultDocRole,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, room)
}

func (h *RoomHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	rooms, err := h.svc.List(r.Context(), actor)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rooms))
}

func (h *RoomHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	room, err := h.svc.Get(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, room)
}

// Discover searches public rooms by name: GET /rooms/discover?q=plans.
func (h *RoomHandler) Discover(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	rooms, err := h.svc.Discover(r.Context(), actor, r.URL.Query().Get("q"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rooms))
}

// Preview shows a public room (or one the caller is in) before joining.
func (h *RoomHandler) Preview(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	summary, err := h.svc.Preview(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// updateRoomRequest uses pointers so an omitted field means "leave unchanged".
type updateRoomRequest struct {
	Name           *string           `json:"name"`
	Visibility     *model.Visibility `json:"visibility"`
	DefaultDocRole *model.DocRoles   `json:"defaultDocRole"`
}

func (h *RoomHandler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	var req updateRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	room, err := h.svc.Update(r.Context(), actor, roomID, service.UpdateRoomInput{
		Name:           req.Name,
		Visibility:     req.Visibility,
		DefaultDocRole: req.DefaultDocRole,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), actor, roomID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}
