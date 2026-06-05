// Package comment is the Comment feature: a second Bun model attached to a Task
// by task_id, with its own store and chi handlers, co-located here.
//
// It exists to show the MULTIPLE-MODELS workflow: loader/main.go registers
// Comment alongside Task, so a single `atlas migrate diff` generates both tables.
//
// task_id is a plain referencing column, not a DB-level foreign key: the Bun
// provider doesn't emit FK constraints from relations, and a hand-added FK would
// drift on the next diff (see "Manual & data migrations" in the migrations
// guide). The relationship is enforced in the application — posting a comment to
// a missing task returns 404.
package comment
