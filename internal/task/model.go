package task

import (
	"time"

	"github.com/uptrace/bun"
)

// Task is the Bun model and the single source of truth Atlas introspects to
// generate migrations. It is a plain struct with bun tags only — no HTTP or
// query logic lives here (see store.go / handler.go).
type Task struct {
	bun.BaseModel `bun:"table:tasks"`

	ID          int64     `bun:"id,pk,autoincrement" json:"id"`
	Title       string    `bun:"title,notnull" json:"title"`
	Description *string   `bun:"description" json:"description"`
	Completed   bool      `bun:"completed,notnull,default:false" json:"completed"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}
