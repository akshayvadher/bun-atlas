// Package task will hold the Task feature: the Bun Task model, the query layer,
// and the chi HTTP handlers, co-located here.
//
// The standalone Atlas provider scans THIS package for Bun models (see
// atlas.hcl: load --path ./internal/task). For now this is an empty placeholder
// so the path exists and compiles; the Task model arrives in Slice 2 and the
// store/handlers in Slice 3.
package task
