// Package comment is the Comment feature: a second Bun model attached to a Task
// by task_id, with its own store and chi handlers, co-located here.
//
// It exists to show the MULTIPLE-MODELS workflow: loader/main.go registers
// Comment alongside Task, so a single `atlas migrate diff` generates both tables.
//
// The comments.task_id -> tasks.id foreign key is added by a MANUAL migration
// (the Bun provider doesn't emit FK constraints), with a diff.skip policy in
// atlas.hcl so Atlas doesn't treat it as drift — see "Manual & data migrations"
// in the migrations guide. The handler also pre-checks the task exists, so a
// comment on a missing task returns a clean 404 rather than a raw FK violation.
package comment
