package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"ba/internal/comment"
	"ba/internal/db"
	"ba/internal/task"
)

const (
	defaultPort        = "8080"
	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable"
	pingTimeout        = 2 * time.Second
)

func main() {
	database := db.Open(databaseURL())

	router := chi.NewRouter()
	router.Get("/healthz", healthzHandler(func(ctx context.Context) error {
		return db.Ping(ctx, database)
	}))
	taskStore := task.NewStore(database)
	task.RegisterRoutes(router, taskStore)
	comment.RegisterRoutes(router, comment.NewStore(database), taskExister{store: taskStore})

	addr := ":" + port()
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}

// taskExister adapts the task store to comment.TaskExister, so the comment
// handler can 404 when a comment is posted to a non-existent task without
// importing the task store's concrete type.
type taskExister struct {
	store task.TaskStore
}

func (t taskExister) TaskExists(ctx context.Context, id int64) (bool, error) {
	_, err := t.store.GetByID(ctx, id)
	if errors.Is(err, task.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// healthzHandler reports 200 when ping succeeds and 503 when it fails. The ping
// is injected so the handler is testable without a live database.
func healthzHandler(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
		defer cancel()

		if err := ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func port() string {
	return envOrDefault("PORT", defaultPort)
}

func databaseURL() string {
	return envOrDefault("DATABASE_URL", defaultDatabaseURL)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
