package task

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// ErrNotFound is returned when a Task with the requested id does not exist.
// Handlers map it to HTTP 404.
var ErrNotFound = errors.New("task not found")

// TaskStore is the persistence seam between the HTTP handlers and the database.
// Handlers depend on this interface, never on *bun.DB directly, so they can be
// tested with an in-memory fake.
type TaskStore interface {
	Create(ctx context.Context, t *Task) (*Task, error)
	List(ctx context.Context) ([]Task, error)
	GetByID(ctx context.Context, id int64) (*Task, error)
	Update(ctx context.Context, t *Task) (*Task, error)
	Delete(ctx context.Context, id int64) error
}

// bunStore is the production TaskStore backed by Bun's query builder. Schema is
// owned by Atlas; this type only reads and writes rows (NewInsert/NewSelect/
// NewUpdate/NewDelete), never DDL.
//
// It depends on bun.IDB (the common interface of *bun.DB, bun.Conn and bun.Tx)
// rather than *bun.DB so the same query code can run inside a test transaction.
type bunStore struct {
	db bun.IDB
}

// NewStore returns a TaskStore backed by the given Bun database. *bun.DB
// satisfies bun.IDB, so production callers pass their *bun.DB unchanged.
func NewStore(db bun.IDB) TaskStore {
	return &bunStore{db: db}
}

func (s *bunStore) Create(ctx context.Context, t *Task) (*Task, error) {
	_, err := s.db.NewInsert().Model(t).Returning("*").Exec(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *bunStore) List(ctx context.Context) ([]Task, error) {
	tasks := []Task{}
	if err := s.db.NewSelect().Model(&tasks).Order("id ASC").Scan(ctx); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *bunStore) GetByID(ctx context.Context, id int64) (*Task, error) {
	t := new(Task)
	err := s.db.NewSelect().Model(t).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *bunStore) Update(ctx context.Context, t *Task) (*Task, error) {
	t.UpdatedAt = time.Now()
	res, err := s.db.NewUpdate().
		Model(t).
		Column("title", "description", "completed", "due_date", "updated_at").
		WherePK().
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, ErrNotFound
	}
	return t, nil
}

func (s *bunStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.NewDelete().Model((*Task)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}
