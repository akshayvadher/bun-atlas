//go:build integration

// Store integration tests exercise the real production Bun query paths
// (NewInsert/NewSelect/NewUpdate/NewDelete inside bunStore, built via NewStore)
// against the live Atlas-migrated Postgres. They follow the same gating
// convention as model_integration_test.go:
//
//  1. The `integration` build tag excludes this file from a plain build.
//  2. Each test skips unless TEST_DATABASE_URL (or DATABASE_URL) is set and the
//     database is reachable.
//
// Each test builds the store over its own transaction (NewStore accepts bun.IDB,
// which bun.Tx satisfies) and rolls the transaction back on cleanup, so the
// suite is repeatable and leaves no rows behind in the app database — while
// still running the exact production query code.
//
// Run with, e.g.:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable \
//	  go test -tags=integration ./internal/task/...
package task

import (
	"context"
	"errors"
	"testing"
)

// newStoreOnTx opens the test database, begins a transaction, and returns the
// production TaskStore bound to that transaction. Cleanup rolls back the
// transaction and closes the pool, so the app DB is left untouched.
func newStoreOnTx(t *testing.T) (TaskStore, context.Context, func()) {
	t.Helper()

	db, closeDB := openTestDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		closeDB()
		t.Fatalf("begin tx: %v", err)
	}

	cleanup := func() {
		_ = tx.Rollback()
		closeDB()
	}

	return NewStore(tx), ctx, cleanup
}

func TestStoreCreatePersistsAndReturnsGeneratedID(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	created, err := store.Create(ctx, &Task{Title: "buy milk"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.ID <= 0 {
		t.Errorf("expected a generated id > 0, got %d", created.ID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be populated by the schema defaults")
	}
	if created.Completed {
		t.Error("expected completed to default to false")
	}
}

func TestStoreListReturnsInsertedRows(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	if _, err := store.Create(ctx, &Task{Title: "first"}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := store.Create(ctx, &Task{Title: "second"}); err != nil {
		t.Fatalf("create second: %v", err)
	}

	tasks, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(tasks) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(tasks))
	}
	titles := map[string]bool{}
	for _, task := range tasks {
		titles[task.Title] = true
	}
	if !titles["first"] || !titles["second"] {
		t.Errorf("expected list to contain inserted rows, got titles %v", titles)
	}
}

func TestStoreGetByIDReturnsTheRow(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	created, err := store.Create(ctx, &Task{Title: "buy milk"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}

	if got.ID != created.ID {
		t.Errorf("expected id %d, got %d", created.ID, got.ID)
	}
	if got.Title != "buy milk" {
		t.Errorf("expected title %q, got %q", "buy milk", got.Title)
	}
}

func TestStoreGetByIDReturnsErrNotFoundForMissing(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	_, err := store.GetByID(ctx, 999999999)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for a missing id, got %v", err)
	}
}

func TestStoreUpdateChangesColumnsAndAdvancesUpdatedAt(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	created, err := store.Create(ctx, &Task{Title: "buy milk"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	desc := "two litres"
	updated, err := store.Update(ctx, &Task{
		ID:          created.ID,
		Title:       "buy oat milk",
		Description: &desc,
		Completed:   true,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Title != "buy oat milk" {
		t.Errorf("expected title %q, got %q", "buy oat milk", updated.Title)
	}
	if !updated.Completed {
		t.Error("expected completed to be updated to true")
	}
	if updated.Description == nil || *updated.Description != desc {
		t.Errorf("expected description %q, got %v", desc, updated.Description)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("expected updated_at to advance past %v, got %v", created.UpdatedAt, updated.UpdatedAt)
	}

	reloaded, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload after update: %v", err)
	}
	if reloaded.Title != "buy oat milk" {
		t.Errorf("expected persisted title %q, got %q", "buy oat milk", reloaded.Title)
	}
}

func TestStoreUpdateReturnsErrNotFoundForMissing(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	_, err := store.Update(ctx, &Task{ID: 999999999, Title: "ghost"})

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound updating a missing row, got %v", err)
	}
}

func TestStoreDeleteRemovesTheRow(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	created, err := store.Create(ctx, &Task{Title: "buy milk"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := store.GetByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestStoreDeleteReturnsErrNotFoundForMissing(t *testing.T) {
	store, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	err := store.Delete(ctx, 999999999)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound deleting a missing row, got %v", err)
	}
}
