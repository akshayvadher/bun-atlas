# Spec: Atlas + Bun CRUD Demo (Tasks)

## Overview
A greenfield Go showcase project demonstrating the *ideal* structure and workflow for managing a PostgreSQL schema with **Atlas** (declarative, versioned migrations generated from Bun models) and querying it with **Bun** (`github.com/uptrace/bun`), exposed as a **REST API** via the **chi** router. The CRUD entity is a `Task`. Primary goal is teaching clarity and a runnable end-to-end `model → migrate diff → migrate apply → query` workflow, not feature breadth.

## Settled decisions (from context, not re-asked)
- Domain entity: **Task / Todo**.
- Interface: **REST API** using `github.com/go-chi/chi/v5`.
- Database: **PostgreSQL**.
- Schema ownership: **Atlas owns the schema** (versioned SQL migrations). Bun is used for **querying only** — the app does NOT create/alter tables with Bun (`db.NewCreateTable()` is forbidden); doing so would defeat the showcase.
- Atlas provider: `ariga.io/atlas-provider-bun` — Bun model structs are the single source of truth; Atlas introspects them via `atlas migrate diff`.

## Confirmed decisions (developer-approved)
1. **Task fields:** minimal set + optional `description` (`id, title, description, completed, created_at, updated_at`).
2. **Schema-change demo:** **YES** — Slice 4 adds `due_date` via a second migration to demonstrate the `atlas migrate diff` workflow.
3. **Postgres provisioning:** **`compose.yaml` run with podman** (`podman compose up`). A NEW compose file dedicated to this project, mapping Postgres to a **non-default host port** (use `5433:5432`) so it never clashes with an existing local Postgres on 5432. `DATABASE_URL` default points at `localhost:5433`.
4. **API validation:** **basic** — 400 for empty title / malformed JSON, 404 for missing id, JSON error bodies.
5. **Atlas provider mode:** ~~standalone~~ → **loader/program mode** (`loader/main.go` registering `&task.Task{}` via `bunschema.New(...).Load(...)`; `atlas.hcl` runs `go run ./loader`; no `tools.go`). *Changed during Slice 4 (developer-approved):* standalone `load --path` mode in `atlas-provider-bun` v0.0.3 treats every exported struct in the scanned package as a table, so the feature-folder's exported `Handler` produced a phantom `CREATE TABLE "handlers"`. Loader mode registers exactly the model and preserves the feature-folder layout.
6. **Command documentation:** ship a **`Taskfile.yml`** (go-task / taskfile.dev) that wraps and documents every workflow command — DB up/down, `atlas migrate diff`, `atlas migrate apply`, run server, etc. This is the runnable "documentation of commands". (Note: the go-task "task" runner and the `Task` domain entity share a name — keep README wording clear about which is which.)

---

## Slice 1: Project skeleton + DB connection (walking skeleton) [x]
A runnable server that connects to Postgres and proves the wiring end-to-end.
- [x] `go run` (or built binary) starts an HTTP server on a configurable port (default `8080`)
- [x] Database connection string is read from an environment variable (e.g. `DATABASE_URL`), with a sensible documented default for local Docker
- [x] A `GET /healthz` endpoint returns 200 and confirms the Postgres connection is reachable (pings the DB)
- [x] `GET /healthz` returns 503 with a JSON error body when the database is unreachable
- [x] `podman compose up` (using a project-local `compose.yaml`) brings up a Postgres instance on host port **5433** the app can connect to using the documented default `DATABASE_URL` (`postgres://...@localhost:5433/...`)
- [x] Project layout follows the ideal Atlas+Bun structure: `atlas.hcl`, `migrations/`, a models package, and the **standalone** Atlas Bun provider wiring (`tools.go`) — documented in the README
- [x] A `Taskfile.yml` exists with tasks that wrap the core commands (at minimum: bring DB up, run the server, `migrate diff`, `migrate apply`); each task is documented so the file doubles as command reference

## Slice 2: Task model + Atlas provider + first migration [x]
The Bun `Task` model becomes the source of truth and Atlas generates the first versioned migration from it.
- [x] A Bun `Task` model struct exists with: `id` (primary key), `title`, `description` (nullable/optional), `completed` (boolean, defaults to false), `created_at`, `updated_at`
- [x] `atlas migrate diff --env bun` generates a versioned SQL migration file plus `atlas.sum` in `migrations/` that creates the `tasks` table matching the model
- [x] `atlas migrate apply --env bun -u <db url>` applies the migration to the running Postgres and creates the `tasks` table
- [x] The application uses Bun only for querying (opens via `pgdriver`/`pgdialect`); it never creates or alters tables at runtime
- [x] README documents the full workflow: edit model → `atlas migrate diff` → `atlas migrate apply` → run app
- [x] Re-running `atlas migrate diff` with no model change produces no new migration (proves the model and schema are in sync)

## Slice 3: Task CRUD REST endpoints [x]
Full CRUD over the Task entity, served through chi and backed by Bun queries. ACs ordered outside-in (user-observable behavior first).
- [x] `POST /tasks` with a valid title creates a task and returns 201 with the created task (including generated `id` and timestamps) as JSON
- [x] A newly created task has `completed = false` by default
- [x] `GET /tasks` returns 200 with a JSON array of all tasks
- [x] `GET /tasks/{id}` returns 200 with the task as JSON when it exists
- [x] `PUT` (or `PATCH`) `/tasks/{id}` updates a task's title/description/completed and returns 200 with the updated task; `updated_at` advances
- [x] `DELETE /tasks/{id}` removes the task and returns 204
- [x] `POST /tasks` with an empty or missing title returns 400 with a JSON error body
- [x] Any endpoint receiving malformed JSON returns 400 with a JSON error body
- [x] `GET`, `PUT`/`PATCH`, and `DELETE` on a non-existent `{id}` return 404 with a JSON error body
- [x] All write queries go through Bun's query builder (`NewInsert` / `NewUpdate` / `NewDelete`) and reads through `NewSelect`

## Slice 4 (optional, recommended): Schema-change migration to demo Atlas diff [x]
Demonstrates Atlas's killer feature — generating an incremental migration from a model change.
- [x] A `due_date` (nullable timestamp) field is added to the `Task` model
- [x] `atlas migrate diff --env bun` generates a SECOND migration that adds the `due_date` column (an `ALTER TABLE`, not a new table)
- [x] `atlas migrate apply --env bun` applies only the new migration to a DB already at the first migration
- [x] The CRUD endpoints accept and return `due_date` (settable on create/update, included in responses)
- [x] README captures the before/after model diff and the two resulting migration files as the worked example of the diff workflow

---

## API Shape (indicative)
```
GET    /healthz            -> 200 {"status":"ok"} | 503 {"error":"..."}
POST   /tasks              {title, description?, due_date?} -> 201 Task
GET    /tasks              -> 200 [Task, ...]
GET    /tasks/{id}         -> 200 Task | 404
PUT    /tasks/{id}         {title?, description?, completed?, due_date?} -> 200 Task | 404
DELETE /tasks/{id}         -> 204 | 404

Task JSON:
{ "id": 1, "title": "...", "description": "...", "completed": false,
  "due_date": null, "created_at": "...", "updated_at": "..." }

Error JSON: { "error": "human-readable message" }
```

## Out of Scope
- Authentication / authorization (no users, no login)
- Pagination, filtering, sorting, or search on the list endpoint
- Soft deletes, audit logging, or task ownership
- Frontend / UI of any kind (REST API only)
- Bun-managed schema (no `db.NewCreateTable()`) — Atlas owns the schema
- Bun's built-in imperative migrations (Atlas is the migration tool for this showcase)
- Production concerns: TLS, rate limiting, observability/metrics, deployment manifests
- Rolling back / `atlas migrate down` demonstrations (forward-only for this demo)

## Technical Context
- **Module:** `ba`, Go 1.26.4. Platform: Windows 11 / PowerShell; Atlas dev-db and runtime Postgres via Docker.
- **Patterns to follow:** Atlas+Bun official integration (`atlasgo.io/guides/orms/bun`, `github.com/ariga/atlas-provider-bun`). Provider modes — standalone (`tools.go` + `atlas.hcl external_schema` running `ariga.io/atlas-provider-bun load`) or program/loader mode (`loader/main.go` using `bunschema.New(bunschema.DialectPostgres).Load(&models.Task{})`). Dev-db: `docker://postgres/15/dev?search_path=public`. Migration format `{{ sql . "  " }}`.
- **Key dependencies (existing/integrating):** `github.com/go-chi/chi/v5`, `github.com/uptrace/bun` + `bun/dialect/pgdialect` + `bun/driver/pgdriver`, `ariga.io/atlas-provider-bun`. Greenfield — only `go.mod` exists today; nothing to tidy.
- **Suggested directory structure:**
  ```
  ba/
  ├── atlas.hcl
  ├── compose.yaml        (podman; Postgres on host port 5433)
  ├── Taskfile.yml        (go-task; documents/runs all workflow commands)
  ├── go.mod / go.sum
  ├── tools.go            (standalone Atlas Bun provider wiring)
  ├── migrations/         (generated SQL + atlas.sum)
  ├── internal/models/    (Bun Task model — source of truth)
  ├── internal/...        (DB open, chi handlers/router)
  └── cmd/server/main.go  (entrypoint)
  ```
- **Risk level:** LOW (greenfield, no external integrations, no auth, no sensitive data).
