package handler

import (
	"log/slog"
	"net/http"

	"github.com/dhanvin018/client-directory/internal/service"
)

type DocumentHandler struct {
	base
	svc *service.DocumentService
}

func NewDocumentHandler(svc *service.DocumentService, log *slog.Logger) *DocumentHandler {
	return &DocumentHandler{base: newBase(log), svc: svc}
}

type createDocumentRequest struct {
	Title        string `json:"title"`
	PositionRank string `json:"positionRank"` // fractional index, supplied by the client
}

func (h *DocumentHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	var req createDocumentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	doc, err := h.svc.Create(r.Context(), actor, roomID, service.CreateDocumentInput{
		Title:        req.Title,
		PositionRank: req.PositionRank,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

type cloneDocumentRequest struct {
	SourceDocID  string  `json:"sourceDocId"`
	Title        *string `json:"title"` // optional
	PositionRank string  `json:"positionRank"`
}

func (h *DocumentHandler) Clone(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	var req cloneDocumentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sourceID, ok := parseUUID(w, "sourceDocId", req.SourceDocID)
	if !ok {
		return
	}
	doc, err := h.svc.Clone(r.Context(), actor, roomID, service.CloneDocumentInput{
		SourceDocID:  sourceID,
		Title:        req.Title,
		PositionRank: req.PositionRank,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (h *DocumentHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	docs, err := h.svc.List(r.Context(), actor, roomID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(docs))
}

func (h *DocumentHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	docID, ok := pathUUID(w, r, "docId")
	if !ok {
		return
	}
	doc, err := h.svc.Get(r.Context(), actor, roomID, docID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// updateDocumentRequest uses pointers so an omitted field means "leave unchanged".
type updateDocumentRequest struct {
	Title        *string `json:"title"`
	PositionRank *string `json:"positionRank"`
}

func (h *DocumentHandler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	docID, ok := pathUUID(w, r, "docId")
	if !ok {
		return
	}
	var req updateDocumentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	doc, err := h.svc.Update(r.Context(), actor, roomID, docID, service.UpdateDocumentInput{
		Title:        req.Title,
		PositionRank: req.PositionRank,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *DocumentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorID(w, r)
	if !ok {
		return
	}
	roomID, ok := pathUUID(w, r, "roomId")
	if !ok {
		return
	}
	docID, ok := pathUUID(w, r, "docId")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), actor, roomID, docID); err != nil {
		h.fail(w, r, err)
		return
	}
	noContent(w)
}
