package router

import "net/http"

// DocumentHandler covers DocumentRef CRUD inside a room. The directory stores
// references and metadata only, never document content.
type DocumentHandler interface {
	Create(w http.ResponseWriter, r *http.Request) // {roomId}; emits CREATE_DOC
	Clone(w http.ResponseWriter, r *http.Request)  // {roomId}; body names the source doc; emits CLONE_DOC
	List(w http.ResponseWriter, r *http.Request)   // {roomId}; ordered by position_rank
	Get(w http.ResponseWriter, r *http.Request)    // {roomId}, {docId}
	Update(w http.ResponseWriter, r *http.Request) // title (RENAME_DOC) and/or positionRank (reorder)
	Delete(w http.ResponseWriter, r *http.Request) // emits DELETE_DOC
}

func registerDocumentRoutes(api group, h DocumentHandler) {
	api.handle("POST /rooms/{roomId}/documents", h.Create)
	api.handle("POST /rooms/{roomId}/documents/clone", h.Clone)
	api.handle("GET /rooms/{roomId}/documents", h.List)
	api.handle("GET /rooms/{roomId}/documents/{docId}", h.Get)
	api.handle("PATCH /rooms/{roomId}/documents/{docId}", h.Update)
	api.handle("DELETE /rooms/{roomId}/documents/{docId}", h.Delete)
}
