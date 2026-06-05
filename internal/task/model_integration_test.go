//go:build integration

// Package task integration tests verify that the Bun Task model round-trips
// against the real Atlas-migrated Postgres schema. They are gated two ways so
// that a plain `go test ./...` stays green without a database:
//
//  1. The `integration` build tag excludes this file from normal builds.
//  2. Even when built with the tag, each test skips unless TEST_DATABASE_URL
//     (or DATABASE_URL) is set.
//
// Run with, e.g.:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable \
//	  go test -tags=integration ./internal/task/...
package task

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// openTestDB opens a Bun connection to the test database, or skips the test
// when no database URL is configured. The returned cleanup closes the pool.
func openTestDB(t *testing.T) (*bun.DB, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL (or DATABASE_URL) to run model integration tests")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	db := bun.NewDB(sqldb, pgdialect.New())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("database at configured URL is not reachable: %v", err)
	}

	return db, func() { _ = db.Close() }
}

// insertAndReload inserts the given task inside a transaction, reloads it from
// the database by its generated id, and returns the persisted copy. The work is
// done in a transaction that the caller rolls back via cleanup, so the test
// leaves no rows behind and is repeatable.
func insertAndReload(t *testing.T, db *bun.DB, toInsert *Task) (*Task, func()) {
	t.Helper()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	cleanup := func() { _ = tx.Rollback() }

	if _, err := tx.NewInsert().Model(toInsert).Returning("*").Exec(ctx); err != nil {
		cleanup()
		t.Fatalf("insert task: %v", err)
	}

	reloaded := new(Task)
	if err := tx.NewSelect().Model(reloaded).Where("id = ?", toInsert.ID).Scan(ctx); err != nil {
		cleanup()
		t.Fatalf("select task back by id: %v", err)
	}

	return reloaded, cleanup
}

func TestInsertedTaskGetsGeneratedID(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk"})
	defer cleanup()

	if persisted.ID <= 0 {
		t.Errorf("expected generated ID > 0, got %d", persisted.ID)
	}
}

func TestInsertedTaskPreservesTitle(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk"})
	defer cleanup()

	if persisted.Title != "buy milk" {
		t.Errorf("expected title %q, got %q", "buy milk", persisted.Title)
	}
}

func TestInsertedTaskDefaultsToNotCompleted(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk"})
	defer cleanup()

	if persisted.Completed {
		t.Error("expected a newly inserted task to default to completed = false")
	}
}

func TestInsertedTaskPopulatesTimestamps(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk"})
	defer cleanup()

	if persisted.CreatedAt.IsZero() {
		t.Error("expected created_at to be populated by the schema default")
	}
	if persisted.UpdatedAt.IsZero() {
		t.Error("expected updated_at to be populated by the schema default")
	}
}

func TestNilDescriptionRoundTripsAsNull(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk", Description: nil})
	defer cleanup()

	if persisted.Description != nil {
		t.Errorf("expected nil description to round-trip as NULL, got %q", *persisted.Description)
	}
}

func TestDescriptionRoundTripsWhenSet(t *testing.T) {
	db, closeDB := openTestDB(t)
	defer closeDB()

	desc := "from the corner shop"
	persisted, cleanup := insertAndReload(t, db, &Task{Title: "buy milk", Description: &desc})
	defer cleanup()

	if persisted.Description == nil {
		t.Fatal("expected description to round-trip, got NULL")
	}
	if *persisted.Description != desc {
		t.Errorf("expected description %q, got %q", desc, *persisted.Description)
	}
}
