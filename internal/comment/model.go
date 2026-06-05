package comment

import (
	"time"

	"github.com/uptrace/bun"
)

// Comment is the second Bun model in this demo, attached to a Task by task_id.
// Like Task, it is a plain struct with bun tags only — Atlas introspects it (via
// loader/main.go) to generate the comments table.
type Comment struct {
	bun.BaseModel `bun:"table:comments"`

	ID        int64     `bun:"id,pk,autoincrement" json:"id"`
	TaskID    int64     `bun:"task_id,notnull" json:"task_id"`
	Body      string    `bun:"body,notnull" json:"body"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}
