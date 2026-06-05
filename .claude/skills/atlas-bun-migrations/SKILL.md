---
name: atlas-bun-migrations
description: Use when changing the database schema in this repo (add/alter/remove a column or table on the Bun Task model, generate or apply an Atlas migration, or debug a failed/dirty migration). Covers the model-is-source-of-truth workflow, what to commit, and the Atlas+Bun+podman gotchas specific to this project.
---

# Atlas + Bun migrations (this repo)

The Bun model is the **single source of truth**. You never hand-write SQL.
Atlas derives versioned migrations from the model; the database consumes them.

- Models: `internal/task/model.go` (`Task`), `internal/comment/model.go`
  (`Comment`, attached to a task by `task_id`).
- Generated migrations: `migrations/*.sql` + `migrations/atlas.sum` (committed).
- Provider: **loader mode** — `loader/main.go` registers **all** models in one
  `Load(&task.Task{}, &comment.Comment{})` call; `atlas.hcl` runs `go run ./loader`.
  (Standalone `--path` mode is NOT used: its scanner treats every exported struct
  as a table and emits phantom tables for the `Handler` types.)
- **Multiple models:** to add a model, add it to that single `Load(...)` call —
  one `migrate diff` then regenerates the schema for all of them.
- Schema ownership: **Atlas owns DDL. Bun is query-only.** Never call
  `db.NewCreateTable()` / runtime DDL.

Full reference with diagrams: `docs/migrations-guide.md`.

## Prerequisites

- `atlas`, `podman`, and `go-task` (`task`) on PATH.
- Bring the databases up first: `task db-up` starts BOTH the app DB (5433) and
  the **dev DB (5434)**. The dev DB is required to *generate* a migration
  (Atlas resets it to compute the diff). On podman hosts the dev DB is a real
  container, not `docker://` (no docker CLI).

## Procedure — change the schema (e.g. add a column)

1. **Edit the model** in `internal/task/model.go`. Add/modify the field with a
   `bun:"..."` tag. For a nullable column use a pointer + `nullzero`
   (e.g. `DueDate *time.Time \`bun:"due_date,nullzero"\``). For NOT NULL on a
   table that may already have rows, give a default
   (e.g. `bun:"priority,notnull,default:0"`) — otherwise apply fails on existing data.
2. **Generate the migration** (dev DB must be up). Pass a descriptive name:
   ```bash
   task migrate-diff -- add_due_date_to_tasks
   # = atlas migrate diff add_due_date_to_tasks --env bun
   ```
   This writes a new `migrations/<ts>_<name>.sql` and updates `migrations/atlas.sum`.
3. **Review the generated SQL.** It is mechanical — confirm it does what you
   intend. A *rename* often appears as `DROP COLUMN` + `ADD COLUMN` (data loss):
   hand-edit it to `ALTER TABLE ... RENAME COLUMN`, then run `atlas migrate hash`
   to refresh `atlas.sum`.
4. **Validate / lint.** `task migrate-validate` (`atlas migrate validate`) is the
   free, offline integrity check. `task migrate-lint`
   (`atlas migrate lint --env bun --latest 1`) adds destructive/locking-change
   analysis but, **since Atlas v0.38, requires `atlas login`** (free account) — so
   use it in CI rather than assuming it runs offline.
5. **Expose it** if the API should carry the field: update `createRequest`/
   `updateRequest` DTOs and the `Update` column set in `internal/task/`, and add
   tests.
6. **Test:** `task test` (fast, no DB) and `task test-integration` (against 5433).
7. **Apply locally:** `task migrate-apply` (→ app DB on 5433). Verify with
   `task migrate-status`. Re-running `task migrate-diff` should report the dir is
   in sync (no new file) — proof the model and schema match.

## What to commit (atomically, in ONE commit)

- `internal/task/model.go` (the field change)
- the new `migrations/<ts>_<name>.sql`
- the updated `migrations/atlas.sum`
- any handler/store/test changes

Forgetting the `.sql` causes drift; forgetting `atlas.sum` fails the integrity
check on the next apply.

## Manual & data migrations (INSERT, seeds, custom SQL)

`migrate diff` only generates DDL from the model. For data or raw SQL, write a
migration by hand:

```bash
atlas migrate new <name> --dir "file://migrations"   # empty file
# edit it (e.g. INSERT INTO "tasks" ...)
atlas migrate hash --dir "file://migrations"          # REQUIRED: re-sync atlas.sum
```

Commit the `.sql` + updated `atlas.sum` together.

- **DML (INSERT/UPDATE/DELETE) is safe** — not schema, so the next `migrate diff`
  ignores it. Keep it valid against the schema at that point (diff replays it on
  the dev DB).
- **Manual DDL the Bun model can't express** (trigger, function, view,
  partial/expression index, CHECK) **drifts**: the next `migrate diff` will try to
  DROP it as not-in-the-desired-state. Prefer expressing it on the model; if you
  can't, treat it as unmanaged and review every future diff.
- Directives: `-- atlas:txmode none` (e.g. `CREATE INDEX CONCURRENTLY`),
  `-- atlas:delimiter //` (functions/triggers).

### Foreign keys (and any object the model can't express)

The Bun provider does NOT emit FK constraints, so a foreign key is a manual
migration (see `migrations/20260605140922_add_comments_task_fk.sql`):

```sql
ALTER TABLE "comments" ADD CONSTRAINT "comments_task_id_fkey"
  FOREIGN KEY ("task_id") REFERENCES "tasks" ("id") ON DELETE CASCADE;
```

`migrate hash` + apply as usual. **But** the next `migrate diff` would see the FK
as drift and try to DROP it (it's in the history, not in the model). The fix is a
`diff.skip` policy in `atlas.hcl`'s `env` block:

```hcl
diff { skip { drop_foreign_key = true } }
```

This stops Atlas auto-dropping FKs (trade-off: removing one becomes manual too).
Same pattern for triggers/views/etc. — add by hand, `skip` the matching drop.

## Changing a migration that's created but NOT yet applied

Safe while unapplied everywhere — just keep `atlas.sum` in sync:

- Edit by hand → `atlas migrate hash`, or `atlas migrate edit <version>` (edits + re-hashes).
- Discard it → `atlas migrate rm <version> --dir "file://migrations"` (removes file + re-hashes).
- Regenerate from a changed model → `atlas migrate rm` it, fix the model, `atlas migrate diff <name>`.

Once a migration has run on a shared env it is immutable — fix forward instead.

## How apply works / production

`atlas migrate apply` records applied versions in an `atlas_schema_revisions`
table in the target DB and takes a lock, so it is **idempotent and replica-safe**.
In production it runs as the **migration init container** (`Dockerfile.migrate`,
`atlas migrate apply --env deploy`, URL via `DATABASE_URL`) BEFORE the app
container starts. Never apply migrations from the app itself.

## Failed / dirty migration

1. A failed init container exits non-zero → pod stays `Init:Error`, app never
   starts on a bad schema (intended).
2. Inspect: `kubectl logs <pod> -c migrate` and
   `SELECT version, error, error_stmt FROM atlas_schema_revisions WHERE error IS NOT NULL;`
3. Postgres has transactional DDL (default `--tx-mode file`), so a failed file
   usually rolls back fully → just fix forward and re-apply.
4. **Has the migration run on a shared environment (staging/prod) yet?**
   - **No** → you may edit the `.sql`, then `atlas migrate hash`, and re-run.
   - **Yes** → migrations are immutable. **Fix forward** with a new migration.

## Gotchas (this project)

- **Rolling deploys:** old app pods briefly run against the new schema during a
  rollout. Destructive changes (drop/rename) break them — use **expand/contract**
  across two releases. Additive (nullable) columns are safe immediately.
- **Keep `loader/main.go` registering exactly the real models** — adding another
  model means adding it to the loader's `Load(...)` call.
- **`atlas.sum` merge conflicts:** two branches each adding a migration both change
  the directory `h1:` hash → conflict / mismatch. Resolve by keeping BOTH migration
  files, then `atlas migrate hash` (recomputes the hash) and `atlas migrate validate`.
  If timestamps interleave out of order, `atlas migrate rebase <version>`.
- **dev vs target DB:** the throwaway dev DB is the `dev = ...` attribute in
  `atlas.hcl` (Atlas resets it — never point at real data); the target is whatever
  you pass via `-u/--url` (or `getenv` in the `deploy` env). The dev DB is only
  needed to `diff`/`lint`/`validate`, NOT to `apply`.
- **`updated_at` is set by the app**, not a DB trigger; migrations won't add one.
- **Adopting an existing DB:** `atlas migrate apply --env deploy --baseline <version>`.
