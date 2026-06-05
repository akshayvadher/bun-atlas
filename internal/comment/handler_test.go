package comment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakeStore is an in-memory CommentStore for fast, DB-free handler tests.
type fakeStore struct {
	mu        sync.Mutex
	nextID    int64
	comments  map[int64]Comment
	forcedErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{nextID: 1, comments: make(map[int64]Comment)}
}

func (s *fakeStore) Create(_ context.Context, c *Comment) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	c.ID = s.nextID
	s.nextID++
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c.CreatedAt = now
	c.UpdatedAt = now
	s.comments[c.ID] = *c
	return c, nil
}

func (s *fakeStore) ListByTask(_ context.Context, taskID int64) ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	out := make([]Comment, 0)
	for _, c := range s.comments {
		if c.TaskID == taskID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeStore) Delete(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return s.forcedErr
	}
	if _, ok := s.comments[id]; !ok {
		return ErrNotFound
	}
	delete(s.comments, id)
	return nil
}

// fakeTasks is a fake TaskExister: a set of existing task ids (+ optional error).
type fakeTasks struct {
	exists map[int64]bool
	err    error
}

func (f fakeTasks) TaskExists(_ context.Context, id int64) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.exists[id], nil
}

func tasksExisting(ids ...int64) fakeTasks {
	m := map[int64]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return fakeTasks{exists: m}
}

func newTestServer(store CommentStore, tasks TaskExister) http.Handler {
	r := chi.NewRouter()
	RegisterRoutes(r, store, tasks)
	return r
}

func do(t *testing.T, srv http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func decodeComment(t *testing.T, rec *httptest.ResponseRecorder) Comment {
	t.Helper()
	var c Comment
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode comment: %v (body=%q)", err, rec.Body.String())
	}
	return c
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v (body=%q)", err, rec.Body.String())
	}
	msg, ok := body["error"]
	if !ok {
		t.Fatalf("expected an \"error\" field, got %q", rec.Body.String())
	}
	return msg
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

// --- create ---

func TestCreateOnExistingTaskReturns201WithComment(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodPost, "/tasks/1/comments", `{"body":"looks good"}`)
	got := decodeComment(t, rec)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got.ID <= 0 {
		t.Errorf("expected a generated id > 0, got %d", got.ID)
	}
	if got.TaskID != 1 {
		t.Errorf("expected task_id 1, got %d", got.TaskID)
	}
	if got.Body != "looks good" {
		t.Errorf("expected body %q, got %q", "looks good", got.Body)
	}
}

func TestCreateOnMissingTaskReturns404(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodPost, "/tasks/999/comments", `{"body":"orphan"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a comment on a missing task, got %d", rec.Code)
	}
	if errorMessage(t, rec) == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestCreateRejectsEmptyBody(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodPost, "/tasks/1/comments", `{"body":"   "}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if errorMessage(t, rec) == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestCreateRejectsMalformedJSON(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodPost, "/tasks/1/comments", `{"body":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCreateWhenStoreFailsReturns500(t *testing.T) {
	store := newFakeStore()
	store.forcedErr = errors.New("connection refused")
	srv := newTestServer(store, tasksExisting(1))

	rec := do(t, srv, http.MethodPost, "/tasks/1/comments", `{"body":"x"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if errorMessage(t, rec) == "" {
		t.Error("expected a non-empty error message")
	}
}

// --- list ---

func TestListReturnsOnlyThisTasksComments(t *testing.T) {
	store := newFakeStore()
	if _, err := store.Create(context.Background(), &Comment{TaskID: 1, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), &Comment{TaskID: 1, Body: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), &Comment{TaskID: 2, Body: "other task"}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(store, tasksExisting(1, 2))

	rec := do(t, srv, http.MethodGet, "/tasks/1/comments", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var comments []Comment
	if err := json.NewDecoder(rec.Body).Decode(&comments); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments for task 1, got %d", len(comments))
	}
	if comments[0].Body != "first" || comments[1].Body != "second" {
		t.Errorf("expected [first, second], got [%q, %q]", comments[0].Body, comments[1].Body)
	}
}

func TestListOnMissingTaskReturns404(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodGet, "/tasks/999/comments", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// --- delete ---

func TestDeleteRemovesComment(t *testing.T) {
	store := newFakeStore()
	created, err := store.Create(context.Background(), &Comment{TaskID: 1, Body: "to delete"})
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(store, tasksExisting(1))

	rec := do(t, srv, http.MethodDelete, "/comments/"+itoa(created.ID), "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if err := store.Delete(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected the comment to be gone, got %v", err)
	}
}

func TestDeleteMissingReturns404(t *testing.T) {
	srv := newTestServer(newFakeStore(), tasksExisting(1))

	rec := do(t, srv, http.MethodDelete, "/comments/999", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if errorMessage(t, rec) == "" {
		t.Error("expected a non-empty error message")
	}
}
