package comment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// TaskExister reports whether a task exists. It is defined here (rather than
// importing the task package) so the dependency points one way and the handler
// is testable with a fake. cmd/server adapts task.TaskStore to satisfy it.
type TaskExister interface {
	TaskExists(ctx context.Context, taskID int64) (bool, error)
}

// Handler exposes the Comment endpoints over a CommentStore + a TaskExister.
type Handler struct {
	store CommentStore
	tasks TaskExister
}

// NewHandler builds a Handler over the given store and task-existence check.
func NewHandler(store CommentStore, tasks TaskExister) *Handler {
	return &Handler{store: store, tasks: tasks}
}

// RegisterRoutes mounts comment endpoints: comments are nested under a task,
// and deletion is addressed by the comment's own id.
func RegisterRoutes(r chi.Router, store CommentStore, tasks TaskExister) {
	h := NewHandler(store, tasks)
	r.Route("/tasks/{taskID}/comments", func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
	})
	r.Delete("/comments/{id}", h.delete)
}

type createRequest struct {
	Body string `json:"body"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	taskID, ok := h.requireTask(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	created, err := h.store.Create(r.Context(), &Comment{TaskID: taskID, Body: req.Body})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	taskID, ok := h.requireTask(w, r)
	if !ok {
		return
	}
	comments, err := h.store.ListByTask(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "comment not found")
		return
	}
	switch err := h.store.Delete(r.Context(), id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "comment not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// requireTask parses {taskID} and 404s when the task does not exist, so a
// comment on a missing task returns a clean 404 instead of a raw foreign-key
// violation from the DB (there IS a comments.task_id -> tasks.id FK, added by a
// manual migration; this check just gives a nicer error).
func (h *Handler) requireTask(w http.ResponseWriter, r *http.Request) (int64, bool) {
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return 0, false
	}
	exists, err := h.tasks.TaskExists(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return 0, false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "task not found")
		return 0, false
	}
	return taskID, true
}

// decodeJSON / writeError / writeJSON intentionally mirror the helpers in
// package task and package main — separate packages, a 4-line helper isn't worth
// a shared httputil. (If a third concern needs them, extract internal/httpx.)
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON request body")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
