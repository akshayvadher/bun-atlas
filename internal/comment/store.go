package comment

import (
	"context"
	"errors"

	"github.com/uptrace/bun"
)

// ErrNotFound is returned when a Comment with the requested id does not exist.
var ErrNotFound = errors.New("comment not found")

// CommentStore is the persistence seam for comments. Handlers depend on this
// interface, not on *bun.DB, so they can be tested with an in-memory fake.
type CommentStore interface {
	Create(ctx context.Context, c *Comment) (*Comment, error)
	ListByTask(ctx context.Context, taskID int64) ([]Comment, error)
	Delete(ctx context.Context, id int64) error
}

// bunStore is the production CommentStore backed by Bun's query builder. It
// depends on bun.IDB so the same query code runs inside a rolled-back test tx.
type bunStore struct {
	db bun.IDB
}

// NewStore returns a CommentStore backed by the given Bun database.
func NewStore(db bun.IDB) CommentStore {
	return &bunStore{db: db}
}

func (s *bunStore) Create(ctx context.Context, c *Comment) (*Comment, error) {
	_, err := s.db.NewInsert().Model(c).Returning("*").Exec(ctx)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *bunStore) ListByTask(ctx context.Context, taskID int64) ([]Comment, error) {
	comments := []Comment{}
	err := s.db.NewSelect().Model(&comments).Where("task_id = ?", taskID).Order("id ASC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func (s *bunStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.NewDelete().Model((*Comment)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}
