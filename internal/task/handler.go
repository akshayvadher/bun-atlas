package task

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Handler exposes the Task CRUD endpoints over a TaskStore. It depends on the
// interface, not on *bun.DB, so it can be tested with an in-memory fake.
type Handler struct {
	store TaskStore
}

// NewHandler builds a Handler over the given store.
func NewHandler(store TaskStore) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes mounts the Task CRUD endpoints under /tasks on r.
func RegisterRoutes(r chi.Router, store TaskStore) {
	h := NewHandler(store)
	r.Route("/tasks", func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})
}

// createRequest is the accepted body for POST /tasks. Binding a DTO (rather than
// the model) keeps client input from setting id or timestamps.
type createRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

// updateRequest is the accepted body for PUT /tasks/{id}.
type updateRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Completed   bool    `json:"completed"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	created, err := h.store.Create(r.Context(), &Task{
		Title:       req.Title,
		Description: req.Description,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	found, err := h.store.GetByID(r.Context(), id)
	if respondStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, found)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	updated, err := h.store.Update(r.Context(), &Task{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		Completed:   req.Completed,
	})
	if respondStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if respondStoreError(w, h.store.Delete(r.Context(), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseID reads {id} from the path; a non-numeric id cannot match an existing
// row, so it is treated as not found.
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return 0, false
	}
	return id, true
}

// decodeJSON parses the request body into dst, rejecting malformed JSON with a
// 400 and returning false so the caller stops.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON request body")
		return false
	}
	return true
}

// respondStoreError maps ErrNotFound to 404 and any other error to 500. It
// returns true when it wrote a response.
func respondStoreError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "task not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON intentionally duplicates the same helper in package main. The two
// live in separate packages and a 4-line helper isn't worth a shared httputil.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
