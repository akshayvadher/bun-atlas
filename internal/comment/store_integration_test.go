//go:build integration

// Store integration tests for comments run the real Bun query paths against the
// live Atlas-migrated Postgres. Same gating as the task package:
//
//  1. The `integration` build tag excludes this file from a plain build.
//  2. Each test skips unless TEST_DATABASE_URL (or DATABASE_URL) is set and the
//     database is reachable.
//
// Each test runs inside a transaction that is rolled back, so the app DB is left
// untouched. Comments have a foreign key to tasks (added by a manual migration),
// so each test seeds a real task row first.
//
// Run with, e.g.:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable \
//	  go test -tags=integration ./internal/comment/...
package comment

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func newStoreOnTx(t *testing.T) (CommentStore, bun.IDB, context.Context, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL (or DATABASE_URL) to run comment integration tests")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	db := bun.NewDB(sqldb, pgdialect.New())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("database not reachable: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		_ = db.Close()
		t.Fatalf("begin tx: %v", err)
	}
	cleanup := func() {
		_ = tx.Rollback()
		_ = db.Close()
	}
	return NewStore(tx), tx, context.Background(), cleanup
}

// seedTask inserts a real tasks row inside the test transaction and returns its
// id, so comments can satisfy the comments.task_id -> tasks.id foreign key.
func seedTask(t *testing.T, tx bun.IDB, ctx context.Context, title string) int64 {
	t.Helper()
	var id int64
	if err := tx.NewRaw(`INSERT INTO tasks (title) VALUES (?) RETURNING id`, title).Scan(ctx, &id); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return id
}

func TestStoreCreatePersistsComment(t *testing.T) {
	store, tx, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	taskID := seedTask(t, tx, ctx, "parent task")
	created, err := store.Create(ctx, &Comment{TaskID: taskID, Body: "nice work"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID <= 0 {
		t.Errorf("expected a generated id > 0, got %d", created.ID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("expected timestamps populated by schema defaults")
	}

	list, err := store.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID || list[0].Body != "nice work" {
		t.Errorf("expected the created comment to be listed for its task, got %+v", list)
	}
}

func TestStoreListByTaskFiltersByTask(t *testing.T) {
	store, tx, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	taskA := seedTask(t, tx, ctx, "task A")
	taskB := seedTask(t, tx, ctx, "task B")
	if _, err := store.Create(ctx, &Comment{TaskID: taskA, Body: "a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, &Comment{TaskID: taskA, Body: "a2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, &Comment{TaskID: taskB, Body: "b1"}); err != nil {
		t.Fatal(err)
	}

	a, err := store.ListByTask(ctx, taskA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(a) != 2 {
		t.Fatalf("expected 2 comments for task A, got %d", len(a))
	}
	if a[0].Body != "a1" || a[1].Body != "a2" {
		t.Errorf("expected [a1, a2] in id order, got [%q, %q]", a[0].Body, a[1].Body)
	}
}

func TestStoreDeleteRemovesComment(t *testing.T) {
	store, tx, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	taskID := seedTask(t, tx, ctx, "parent task")
	created, err := store.Create(ctx, &Comment{TaskID: taskID, Body: "delete me"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound deleting an already-removed comment, got %v", err)
	}
}

func TestStoreDeleteReturnsErrNotFoundForMissing(t *testing.T) {
	store, _, ctx, cleanup := newStoreOnTx(t)
	defer cleanup()

	if err := store.Delete(ctx, 999999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
