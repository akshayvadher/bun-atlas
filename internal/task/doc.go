// Package task is the Task feature package: the Bun Task model, the TaskStore
// query layer, and the chi HTTP handlers are co-located here.
//
// The Bun Task model (in model.go) is the single source of truth for the
// schema. The Atlas loader (loader/main.go) registers it explicitly via
// bunschema.New(bunschema.DialectPostgres).Load(&task.Task{}); Atlas does not
// scan this package. Atlas owns the schema (it generates and applies
// migrations from the model), so Bun here is query-only — it never creates or
// alters tables.
package task
