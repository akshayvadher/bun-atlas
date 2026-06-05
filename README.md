# Atlas + Bun CRUD Demo (Tasks)

A small Go showcase for the *ideal* workflow of managing a PostgreSQL schema with
**Atlas** (declarative, versioned migrations) while querying it with **Bun**
(`github.com/uptrace/bun`), exposed as a REST API via **chi**. The CRUD entity is
a `Task`.

> **Naming note:** this project uses the **go-task** runner (`Taskfile.yml`, the
> `task` command). That is *not* the same thing as the `Task` domain entity the
> API manages. "Run a task" = run a go-task command; "a Task" = a row in the
> `tasks` table.

## Stack

| Concern              | Tool                                                            |
| -------------------- | --------------------------------------------------------------- |
| HTTP router          | `github.com/go-chi/chi/v5`                                      |
| Query layer          | `github.com/uptrace/bun` (+ `pgdialect`, `pgdriver`)            |
| Schema / migrations  | [Atlas](https://atlasgo.io) via `ariga.io/atlas-provider-bun`  |
| Database             | PostgreSQL 15 (podman/docker compose)                          |
| Command documentation| go-task (`Taskfile.yml`)                                        |

### Schema ownership: Atlas owns the schema, Bun is query-only

This is the whole point of the demo. The Bun `Task` model struct (in
`internal/task/model.go`) is the **single source of truth**. Atlas
introspects that struct and generates versioned SQL migrations. The application
**never** creates or alters tables at runtime — `db.NewCreateTable()` and any
runtime DDL are forbidden. Bun is used only for `NewSelect` / `NewInsert` /
`NewUpdate` / `NewDelete`.

### Atlas provider: program/loader mode

We use the **program/loader** provider mode. `atlas.hcl`'s `external_schema`
runs a tiny Go program (`./loader`) that registers exactly the models Atlas
should introspect and prints their DDL:

```go
// loader/main.go
stmts, err := bunschema.New(bunschema.DialectPostgres).Load(&task.Task{})
```

- `atlas.hcl` defines env `bun`, whose `external_schema` runs
  `go run -mod=mod ./loader`.
- The loader imports `ariga.io/atlas-provider-bun/bunschema` directly, so
  `go mod tidy` keeps the provider in `go.mod` — no separate `tools.go` pin is
  needed.

> **Why program mode and not standalone `load --path ./internal/task`?**
> The standalone scanner (`atlas-provider-bun` v0.0.3) treats **every exported
> struct in the package** as a table — it has no way to select only the models.
> Because the `task` package co-locates the exported `Handler` type with the
> `Task` model, standalone mode would emit a phantom `CREATE TABLE "handlers"`
> into the diff. Program mode lets us list the real source-of-truth model
> (`&task.Task{}`) explicitly, keeping the generated schema — and therefore the
> migration — clean.

## Project layout

```
ba/
├── atlas.hcl            # Atlas env "bun": external_schema runs ./loader; dev = Postgres on 5434
├── compose.yaml         # Two Postgres 15 services: app DB (5433) + Atlas dev DB (5434)
├── Taskfile.yml         # go-task: db-up, db-down, run, migrate-diff, migrate-apply, migrate-status
├── loader/main.go       # Atlas provider in program mode: registers ALL models (Task, Comment)
├── go.mod / go.sum
├── migrations/          # Atlas-generated SQL + atlas.sum (created by `atlas migrate diff`)
├── internal/
│   ├── task/            # the Task feature (model + store + chi handlers)
│   │   ├── model.go     # the Bun Task model — a source of truth Atlas introspects
│   │   ├── store.go     # TaskStore interface + bunStore (NewInsert/NewSelect/NewUpdate/NewDelete)
│   │   └── handler.go   # chi handlers + RegisterRoutes; depends on TaskStore, never *bun.DB
│   ├── comment/         # the Comment feature — a SECOND model attached to a task by task_id
│   │   ├── model.go     # the Bun Comment model (also registered in loader/main.go)
│   │   ├── store.go     # CommentStore interface + bunStore
│   │   └── handler.go   # chi handlers; 404s a comment posted to a missing task
│   └── db/db.go         # Open(dsn) -> *bun.DB via pgdriver/pgdialect; Ping() for /healthz
└── cmd/server/main.go   # entrypoint: read env, open db, mount chi router (/healthz + /tasks + comments), serve
```

### Multiple models

Every Bun model the schema should contain is listed in the single `Load(...)`
call in `loader/main.go`:

```go
bunschema.New(bunschema.DialectPostgres).Load(&task.Task{}, &comment.Comment{})
```

Adding a model means adding it there — then **one** `atlas migrate diff`
regenerates the schema for all of them (the `comments` table arrived this way).
`task_id` on a comment is a plain referencing column, **not** a DB-level foreign
key: the Bun provider doesn't emit FK constraints, and a hand-added FK would
drift on the next diff (see *Manual & data migrations* in the migrations guide).
The relationship is enforced by the app — a comment on a missing task returns 404.

## Configuration

| Env var        | Default                                                              | Notes                                  |
| -------------- | ------------------------------------------------------------------- | -------------------------------------- |
| `PORT`         | `8080`                                                              | HTTP listen port                       |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable` | Matches `compose.yaml`; host port 5433 |

Host port **5433** (mapped to the container's 5432) is intentional so it never
clashes with a Postgres already running on the default 5432.

### Two databases: app DB vs. Atlas dev DB

`compose.yaml` starts **two** Postgres containers:

| Service        | Host port | Role                                                                                                |
| -------------- | --------- | --------------------------------------------------------------------------------------------------- |
| `postgres`     | **5433**  | **App database.** Migrations are applied here; the server connects here. Data persists in a volume. |
| `postgres-dev` | **5434**  | **Atlas dev (scratch) database.** Atlas resets it on every `migrate diff` to compute the schema diff. Holds no app data (tmpfs). |

Atlas needs a throwaway "dev" database to normalize and diff schemas. The usual
`dev = "docker://postgres/15/dev"` in `atlas.hcl` spins one up via the **Docker
CLI** — but this project targets **podman**, which has no `docker` binary. So
`atlas.hcl` instead points `dev` at the real `postgres-dev` container:

```hcl
dev = "postgres://postgres:postgres@localhost:5434/dev?sslmode=disable&search_path=public"
```

`task db-up` (i.e. `podman compose up -d`) brings up **both** DBs. You need the
**dev DB (5434) up to run `migrate diff`**, and the **app DB (5433) up to run
`migrate apply`**.

## Prerequisites

- Go 1.26.4
- [podman](https://podman.io) (or Docker) for Postgres + the Atlas dev database
- [Atlas CLI](https://atlasgo.io/getting-started#installation)
- [go-task](https://taskfile.dev) (optional; commands also work standalone)

## Running it

Using go-task:

```powershell
task db-up          # start BOTH Postgres containers (app DB 5433 + dev DB 5434)
task migrate-diff   # generate a migration from the Bun model (uses dev DB 5434)
task migrate-apply  # apply migrations to the app DB (5433)
task migrate-status # show applied/pending migrations on the app DB
task run            # start the HTTP server
task test           # run the fast unit tests (no DB required)
task test-integration # run the DB integration tests against the app DB (5433)
```

Equivalent raw commands:

```powershell
podman compose up -d
atlas migrate diff  --env bun
atlas migrate apply --env bun -u "postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable"
go run ./cmd/server
```

Health check:

```powershell
curl http://localhost:8080/healthz
# DB reachable    -> 200 {"status":"ok"}
# DB unreachable  -> 503 {"error":"..."}
```

## Task CRUD API

All endpoints are served by chi and backed by Bun queries. Writes go through
`NewInsert`/`NewUpdate`/`NewDelete`; reads through `NewSelect`. Error bodies are
always `{"error": "message"}`.

| Method   | Path          | Body                                   | Success         | Errors                                  |
| -------- | ------------- | -------------------------------------- | --------------- | --------------------------------------- |
| `POST`   | `/tasks`      | `{title, description?, due_date?}`             | `201` Task      | `400` empty title / malformed JSON      |
| `GET`    | `/tasks`      | —                                              | `200` `[Task]`  | —                                       |
| `GET`    | `/tasks/{id}` | —                                              | `200` Task      | `404` missing id                        |
| `PUT`    | `/tasks/{id}` | `{title, description?, completed, due_date?}`  | `200` Task      | `400` empty title / malformed; `404`    |
| `DELETE` | `/tasks/{id}` | —                                              | `204`           | `404` missing id                        |

The request body is a small DTO: client-supplied `id`/`created_at`/`updated_at`
are ignored. A new task defaults to `completed = false`. `due_date` is optional
and nullable — omit it (or send `null`) to leave it unset; it round-trips on
both create and update. `PUT` advances `updated_at`.

Task JSON:

```json
{
  "id": 1,
  "title": "Write the docs",
  "description": "cover the CRUD endpoints",
  "completed": false,
  "due_date": "2026-07-01T09:00:00Z",
  "created_at": "2026-06-05T10:43:06Z",
  "updated_at": "2026-06-05T10:43:06Z"
}
```

### Example calls (PowerShell / Invoke-RestMethod)

```powershell
$base = "http://localhost:8080"

# Create (201)
$task = Invoke-RestMethod -Uri "$base/tasks" -Method Post `
  -ContentType 'application/json' `
  -Body '{"title":"Write the docs","description":"cover the CRUD endpoints"}'

# List (200)
Invoke-RestMethod -Uri "$base/tasks" -Method Get

# Get one (200, or 404 if missing)
Invoke-RestMethod -Uri "$base/tasks/$($task.id)" -Method Get

# Update (200; updated_at advances)
Invoke-RestMethod -Uri "$base/tasks/$($task.id)" -Method Put `
  -ContentType 'application/json' `
  -Body '{"title":"Write the docs","description":"done","completed":true}'

# Delete (204)
Invoke-RestMethod -Uri "$base/tasks/$($task.id)" -Method Delete
```

### Example calls (curl)

```bash
# Create (201)
curl -i -X POST http://localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Write the docs","description":"cover the CRUD endpoints"}'

# List (200)
curl http://localhost:8080/tasks

# Get one (200 / 404)
curl -i http://localhost:8080/tasks/1

# Update (200)
curl -i -X PUT http://localhost:8080/tasks/1 \
  -H 'Content-Type: application/json' \
  -d '{"title":"Write the docs","description":"done","completed":true}'

# Delete (204)
curl -i -X DELETE http://localhost:8080/tasks/1
```

## Comments API (the second model)

Comments are attached to a task by `task_id` and demonstrate the multiple-models
workflow. Same conventions as tasks (chi + Bun, `{"error": ...}` bodies).

| Method   | Path                       | Body       | Success            | Errors                                  |
| -------- | -------------------------- | ---------- | ------------------ | --------------------------------------- |
| `POST`   | `/tasks/{taskID}/comments` | `{body}`   | `201` Comment      | `400` empty body / malformed; `404` task |
| `GET`    | `/tasks/{taskID}/comments` | —          | `200` `[Comment]`  | `404` task missing                      |
| `DELETE` | `/comments/{id}`           | —          | `204`              | `404` missing id                        |

Posting or listing under a task that doesn't exist returns `404` (the app
enforces the relationship, since `task_id` is not a DB foreign key).

```json
{ "id": 1, "task_id": 7, "body": "looks good", "created_at": "…", "updated_at": "…" }
```

```bash
# Comment on task 7 (201; 404 if task 7 doesn't exist)
curl -i -X POST http://localhost:8080/tasks/7/comments \
  -H 'Content-Type: application/json' -d '{"body":"looks good"}'

# List task 7's comments (200)
curl http://localhost:8080/tasks/7/comments

# Delete comment 1 (204)
curl -i -X DELETE http://localhost:8080/comments/1
```

## The edit-model → diff → apply → run workflow

This is the loop the demo teaches:

1. **Edit the model.** Change the Bun `Task` struct in `internal/task/model.go`.
2. **Diff.** `atlas migrate diff --env bun` — Atlas loads the model via the
   `./loader` program, compares it against the **dev database** (`postgres-dev`
   on 5434, which Atlas resets), and writes a new timestamped SQL migration plus
   `atlas.sum` into `migrations/`.
3. **Apply.** `atlas migrate apply --env bun -u "$DATABASE_URL"` — applies any
   pending migrations to the **app database** (5433).
4. **Run.** `go run ./cmd/server` — the app queries the now-current schema with
   Bun.

Re-running `migrate diff` with no model change produces no new migration
(`The migration directory is synced with the desired state, no changes to be
made`): that is the proof the model and the schema are in sync.

### Worked example: the first migration

The `Task` model in `internal/task/model.go`:

```go
type Task struct {
	bun.BaseModel `bun:"table:tasks"`

	ID          int64     `bun:"id,pk,autoincrement" json:"id"`
	Title       string    `bun:"title,notnull" json:"title"`
	Description *string   `bun:"description" json:"description"`
	Completed   bool      `bun:"completed,notnull,default:false" json:"completed"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}
```

`atlas migrate diff --env bun` generated `migrations/20260605103003.sql`:

```sql
-- Create "tasks" table
CREATE TABLE "tasks" (
  "id" bigserial NOT NULL,
  "title" character varying NOT NULL,
  "description" character varying NULL,
  "completed" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);
```

### Worked example: the incremental `due_date` migration (the killer feature)

This is the payoff of letting the model drive migrations. We add a single
nullable field to the `Task` model and Atlas computes an **incremental
`ALTER TABLE`** — it does *not* re-create the table.

**The one-line model change** in `internal/task/model.go`:

```diff
  Completed   bool       `bun:"completed,notnull,default:false" json:"completed"`
+ DueDate     *time.Time `bun:"due_date,nullzero" json:"due_date"`
  CreatedAt   time.Time  `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
```

`*time.Time` + `nullzero` makes the column nullable (a `nil` pointer / zero
value maps to SQL `NULL`).

**How Atlas computes the diff.** `atlas migrate diff add_due_date_to_tasks --env bun`:

1. Runs `./loader`, which prints the *desired* schema (the `tasks` table now
   including `due_date`).
2. Replays the **existing** migrations (`20260605103003.sql`) onto the scratch
   **dev database** (`postgres-dev`, 5434) to reconstruct the *current* schema.
3. Diffs desired vs. current and writes only the delta as a new timestamped
   migration — leaving the first migration and its `atlas.sum` hash untouched.

The generated second migration,
`migrations/20260605110617_add_due_date_to_tasks.sql`:

```sql
-- Modify "tasks" table
ALTER TABLE "tasks" ADD COLUMN "due_date" timestamptz NULL;
```

**Applying only the new migration.** The app DB was already at version
`20260605103003`, so `atlas migrate apply` runs *just* the one pending file:

```text
$ atlas migrate status --env bun -u "$DATABASE_URL"
  -- Current Version: 20260605103003
  -- Executed Files:  1
  -- Pending Files:   1

$ atlas migrate apply --env bun -u "$DATABASE_URL"
Migrating to version 20260605110617 from 20260605103003 (1 migrations in total):
  -- migrating version 20260605110617
    -> ALTER TABLE "tasks" ADD COLUMN "due_date" timestamptz NULL;
  -- ok

$ atlas migrate status --env bun -u "$DATABASE_URL"
  -- Current Version: 20260605110617
  -- Executed Files:  2          # went from 1 -> 2
  -- Pending Files:   0
```

The two migration files together are the worked record of the diff workflow:
`20260605103003.sql` creates the table, and
`20260605110617_add_due_date_to_tasks.sql` evolves it — no hand-written SQL, no
Bun runtime DDL.

## Deployment (Docker + Kubernetes)

Two images split the two concerns. **Migrations are applied by a dedicated
image that runs as an init container — never by the app at startup.** This
keeps the app image free of the Atlas binary and keeps schema changes an
explicit, ordered step.

| File                | Image                          | Role                                                                 |
| ------------------- | ------------------------------ | -------------------------------------------------------------------- |
| `Dockerfile`        | app server                     | Multi-stage Go build → static binary on `distroless/static` (nonroot, no shell). Serves `/healthz` + `/tasks`. |
| `Dockerfile.migrate`| migration runner               | `arigaio/atlas` + `migrations/` + `atlas.deploy.hcl`. Runs `atlas migrate apply` and exits. Meant to be a **k8s init container**. |

Why a separate migration image (rather than `atlas migrate diff`'s setup):
`apply` needs only the atlas binary, the generated `migrations/` directory, and
a target `DATABASE_URL`. It does **not** need Go, the Bun loader, or the dev
database — those generate migrations, they don't apply them. Atlas records
applied revisions in an `atlas_schema_revisions` table in your DB, so running
the init container on every pod start is **idempotent**: already-applied
migrations are skipped (it prints `No migration files to execute` and exits 0).

`atlas.deploy.hcl` is an apply-only config that reads the URL from the
environment, so the same image works for `docker run` and k8s without
overriding any command:

```hcl
env "deploy" {
  url = getenv("DATABASE_URL")
  migration { dir = "file:///migrations" }
}
```

### Build

```bash
podman build -f Dockerfile          -t <registry>/bun-atlas-app:<tag> .
podman build -f Dockerfile.migrate  -t <registry>/bun-atlas-migrate:<tag> .
```

### Run locally (sanity check)

```bash
# Apply migrations (idempotent). --network host reaches the compose Postgres on 5433.
podman run --rm --network host \
  -e DATABASE_URL="postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable" \
  <registry>/bun-atlas-migrate:<tag>

# Start the app
podman run --rm --network host \
  -e DATABASE_URL="postgres://postgres:postgres@localhost:5433/tasks?sslmode=disable" \
  -e PORT=8080 \
  <registry>/bun-atlas-app:<tag>
```

### Kubernetes — migration as an init container

The init container runs to completion (applying any pending migrations) before
the app container starts. Both read `DATABASE_URL` from the same Secret.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bun-atlas
spec:
  replicas: 2
  selector:
    matchLabels: { app: bun-atlas }
  template:
    metadata:
      labels: { app: bun-atlas }
    spec:
      initContainers:
        - name: migrate
          image: <registry>/bun-atlas-migrate:<tag>
          # Image CMD already runs: atlas migrate apply --env deploy -c file:///atlas.hcl
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef: { name: bun-atlas-db, key: url }
      containers:
        - name: app
          image: <registry>/bun-atlas-app:<tag>
          ports:
            - containerPort: 8080
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef: { name: bun-atlas-db, key: url }
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 2
          livenessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 5
```

> The DB URL in production should point at your managed Postgres (and usually
> `sslmode=require`), supplied via the `bun-atlas-db` Secret. With multiple
> replicas, the init container still applies migrations safely — Atlas takes a
> lock so concurrent appliers don't race.
