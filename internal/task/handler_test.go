package task

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakeStore is an in-memory TaskStore used to drive the handlers without a
// database. It mimics the observable behaviour the real bunStore relies on the
// Postgres schema for: Create assigns a generated id, stamps created_at /
// updated_at, and leaves completed = false; Update advances updated_at.
type fakeStore struct {
	mu     sync.Mutex
	nextID int64
	tasks  map[int64]Task

	// forcedErr, when set, is returned by every method so error-path mapping
	// (e.g. 500) can be exercised. ErrNotFound is returned by id-based methods
	// when the row is absent regardless of this field.
	forcedErr error

	// updateClock is the timestamp Update stamps onto updated_at. Tests set it
	// to a time strictly after created_at so "updated_at advances" is assertable.
	updateClock time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{nextID: 1, tasks: make(map[int64]Task)}
}

func (s *fakeStore) Create(_ context.Context, t *Task) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	t.ID = s.nextID
	s.nextID++
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t.CreatedAt = now
	t.UpdatedAt = now
	// completed is intentionally left at its zero value (false) — the schema
	// default the real store relies on.
	s.tasks[t.ID] = *t
	return t, nil
}

func (s *fakeStore) List(_ context.Context) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeStore) GetByID(_ context.Context, id int64) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	t, ok := s.tasks[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &t, nil
}

func (s *fakeStore) Update(_ context.Context, t *Task) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return nil, s.forcedErr
	}
	existing, ok := s.tasks[t.ID]
	if !ok {
		return nil, ErrNotFound
	}
	t.CreatedAt = existing.CreatedAt
	advanced := s.updateClock
	if advanced.IsZero() {
		advanced = existing.UpdatedAt.Add(time.Hour)
	}
	t.UpdatedAt = advanced
	s.tasks[t.ID] = *t
	return t, nil
}

func (s *fakeStore) Delete(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forcedErr != nil {
		return s.forcedErr
	}
	if _, ok := s.tasks[id]; !ok {
		return ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}

// newTestServer mounts the task routes over the given store on a chi router.
func newTestServer(store TaskStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutes(r, store)
	return r
}

// do executes a request against the handler and returns the recorder.
func do(t *testing.T, srv http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Buffer
	if body == "" {
		reader = bytes.NewBufferString("")
	} else {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// decodeTask reads a single Task from the response body.
func decodeTask(t *testing.T, rec *httptest.ResponseRecorder) Task {
	t.Helper()
	var task Task
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task body: %v (body=%q)", err, rec.Body.String())
	}
	return task
}

// decodeErrorBody reads the {"error":"..."} body and returns the message.
func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v (body=%q)", err, rec.Body.String())
	}
	msg, ok := body["error"]
	if !ok {
		t.Fatalf("expected error body to contain an \"error\" field, got %q", rec.Body.String())
	}
	return msg
}

// seedTask creates a task directly via the store so id-based tests have a row.
func seedTask(t *testing.T, store TaskStore, title string) Task {
	t.Helper()
	created, err := store.Create(context.Background(), &Task{Title: title})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return *created
}

// --- AC1 / AC2: POST creates a task ---

func TestCreateValidTitleReturns201(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestCreateEchoesTitleInResponse(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if got.Title != "buy milk" {
		t.Errorf("expected title %q in response, got %q", "buy milk", got.Title)
	}
}

func TestCreateReturnsGeneratedID(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if got.ID <= 0 {
		t.Errorf("expected a generated id > 0, got %d", got.ID)
	}
}

func TestCreateReturnsPopulatedTimestamps(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if got.CreatedAt.IsZero() {
		t.Error("expected created_at to be populated in the response")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("expected updated_at to be populated in the response")
	}
}

func TestCreateDefaultsCompletedToFalse(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if got.Completed {
		t.Error("expected a newly created task to have completed = false")
	}
}

func TestCreateAcceptsOptionalDescription(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk","description":"from the shop"}`)
	got := decodeTask(t, rec)

	if got.Description == nil {
		t.Fatal("expected description to be present in the response")
	}
	if *got.Description != "from the shop" {
		t.Errorf("expected description %q, got %q", "from the shop", *got.Description)
	}
}

func TestCreateAcceptsDueDateAndReturnsIt(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk","due_date":"2026-07-01T09:00:00Z"}`)
	got := decodeTask(t, rec)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got.DueDate == nil {
		t.Fatal("expected due_date to be present in the response")
	}
	want := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if !got.DueDate.Equal(want) {
		t.Errorf("expected due_date %v, got %v", want, *got.DueDate)
	}
}

func TestCreateWithoutDueDateReturnsNullDueDateAnd201(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got.DueDate != nil {
		t.Errorf("expected due_date to be null when omitted, got %v", *got.DueDate)
	}
}

func TestUpdateSetsDueDateAndReturnsIt(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), `{"title":"buy milk","due_date":"2026-07-01T09:00:00Z"}`)
	got := decodeTask(t, rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got.DueDate == nil {
		t.Fatal("expected due_date to be present in the response")
	}
	want := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if !got.DueDate.Equal(want) {
		t.Errorf("expected due_date %v, got %v", want, *got.DueDate)
	}
}

// The update handler binds a full updateRequest and passes its DueDate straight
// through, so an absent due_date decodes to nil and the update clears it. This
// documents the actual full-replace semantics rather than assuming a merge.
func TestUpdateClearsDueDateWhenOmitted(t *testing.T) {
	store := newFakeStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	created, err := store.Create(context.Background(), &Task{Title: "buy milk", DueDate: &due})
	if err != nil {
		t.Fatalf("seed task with due_date: %v", err)
	}
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(created.ID), `{"title":"buy milk"}`)
	got := decodeTask(t, rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got.DueDate != nil {
		t.Errorf("expected due_date to be cleared when omitted from the update body, got %v", *got.DueDate)
	}
}

// --- AC3: GET list ---

func TestListReturns200(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodGet, "/tasks", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestListReturnsEmptyArrayWhenNoTasks(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodGet, "/tasks", "")

	var tasks []Task
	if err := json.NewDecoder(rec.Body).Decode(&tasks); err != nil {
		t.Fatalf("expected a JSON array, decode failed: %v (body=%q)", err, rec.Body.String())
	}
	if len(tasks) != 0 {
		t.Errorf("expected an empty array, got %d tasks", len(tasks))
	}
}

func TestListReturnsAllCreatedTasks(t *testing.T) {
	store := newFakeStore()
	seedTask(t, store, "first")
	seedTask(t, store, "second")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodGet, "/tasks", "")

	var tasks []Task
	if err := json.NewDecoder(rec.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode list body: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Title != "first" || tasks[1].Title != "second" {
		t.Errorf("expected [first, second], got [%q, %q]", tasks[0].Title, tasks[1].Title)
	}
}

// --- AC4: GET {id} existing ---

func TestGetExistingReturns200(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodGet, "/tasks/"+itoa(seeded.ID), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestGetExistingReturnsTheTask(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodGet, "/tasks/"+itoa(seeded.ID), "")
	got := decodeTask(t, rec)

	if got.ID != seeded.ID {
		t.Errorf("expected id %d, got %d", seeded.ID, got.ID)
	}
	if got.Title != "buy milk" {
		t.Errorf("expected title %q, got %q", "buy milk", got.Title)
	}
}

// --- AC5: PUT {id} updates and advances updated_at ---

func TestUpdateExistingReturns200(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), `{"title":"buy oat milk","completed":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestUpdateChangesFields(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	desc := "two litres"
	body := `{"title":"buy oat milk","description":"` + desc + `","completed":true}`
	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), body)
	got := decodeTask(t, rec)

	if got.Title != "buy oat milk" {
		t.Errorf("expected updated title %q, got %q", "buy oat milk", got.Title)
	}
	if !got.Completed {
		t.Error("expected completed to be updated to true")
	}
	if got.Description == nil || *got.Description != desc {
		t.Errorf("expected description %q, got %v", desc, got.Description)
	}
}

func TestUpdateAdvancesUpdatedAt(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), `{"title":"buy oat milk"}`)
	got := decodeTask(t, rec)

	if !got.UpdatedAt.After(seeded.UpdatedAt) {
		t.Errorf("expected updated_at to advance past %v, got %v", seeded.UpdatedAt, got.UpdatedAt)
	}
}

// --- AC6: DELETE {id} ---

func TestDeleteExistingReturns204(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodDelete, "/tasks/"+itoa(seeded.ID), "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
}

func TestDeleteRemovesTheTask(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	do(t, srv, http.MethodDelete, "/tasks/"+itoa(seeded.ID), "")
	rec := do(t, srv, http.MethodGet, "/tasks/"+itoa(seeded.ID), "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected the deleted task to be gone (404), got %d", rec.Code)
	}
}

// --- AC7: empty / missing title -> 400 ---

func TestCreateRejectsInvalidTitle(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty title", `{"title":""}`},
		{"whitespace-only title", `{"title":"   "}`},
		{"missing title", `{"description":"no title here"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(newFakeStore())

			rec := do(t, srv, http.MethodPost, "/tasks", tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d", rec.Code)
			}
			if msg := decodeErrorBody(t, rec); msg == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}
}

func TestUpdateRejectsEmptyTitle(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), `{"title":""}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

// --- AC8: malformed JSON -> 400 ---

func TestCreateRejectsMalformedJSON(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPost, "/tasks", `{"title":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestUpdateRejectsMalformedJSON(t *testing.T) {
	store := newFakeStore()
	seeded := seedTask(t, store, "buy milk")
	srv := newTestServer(store)

	rec := do(t, srv, http.MethodPut, "/tasks/"+itoa(seeded.ID), `{"title":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

// --- AC9: operations on a non-existent id -> 404 with error body ---

func TestGetMissingReturns404WithError(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodGet, "/tasks/999", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestUpdateMissingReturns404WithError(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodPut, "/tasks/999", `{"title":"ghost"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestDeleteMissingReturns404WithError(t *testing.T) {
	srv := newTestServer(newFakeStore())

	rec := do(t, srv, http.MethodDelete, "/tasks/999", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	if msg := decodeErrorBody(t, rec); msg == "" {
		t.Error("expected a non-empty error message")
	}
}

// itoa renders an int64 id for use in a request path.
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
