# Migrations Guide — Atlas + Bun

How schema migrations work in this project: how to add a column, what to commit,
what commands to run, how migrations are applied and tracked, how to handle a
failed migration, and the gotchas worth knowing before you hit them.

This complements the [README](../README.md) (which covers the local dev loop and
deployment). Everything here is grounded in *this* project: `internal/task/model.go`
is the source of truth, Atlas runs in **loader mode** with an **ephemeral podman
dev DB** (spun up per `diff`), and migrations are applied in production by a
dedicated init container.

---

## 1. The mental model — one source of truth, two derived artifacts

The single most important idea: **your Bun struct is the source of truth.** You
never write SQL by hand. Atlas *derives* versioned SQL from the struct, and a
database *consumes* that SQL.

```mermaid
flowchart LR
    M["internal/task/model.go<br/><b>Bun Task struct</b><br/>(SOURCE OF TRUTH)"]
    F["migrations/*.sql + atlas.sum<br/><b>versioned history</b><br/>(committed to git)"]
    DB[("PostgreSQL<br/>tasks table")]
    R[("atlas_schema_revisions<br/>which versions are applied")]

    M -->|"atlas migrate diff<br/>(you, at dev time)"| F
    F -->|"atlas migrate apply<br/>(init container, at deploy time)"| DB
    F -.->|"records each version"| R
    DB --- R
```

Two distinct phases, run by two different actors at two different times:

| Phase        | Command              | Who runs it          | When                    | Needs                          |
| ------------ | -------------------- | -------------------- | ----------------------- | ------------------------------ |
| **Generate** | `atlas migrate diff` | you, a developer     | dev time, your machine  | an ephemeral dev DB + the Go loader |
| **Apply**    | `atlas migrate apply`| migration init container | deploy time, in k8s | only the target DB URL         |

This split is why there are two Docker images. The app image never touches schema.

---

## 2. How `migrate diff` actually works (the clever part)

When you change the struct and run `atlas migrate diff`, Atlas needs to answer:
*"what SQL turns the current schema into the desired schema?"* It computes this
using a **throwaway dev database as a scratch pad** — never your real data.

```mermaid
sequenceDiagram
    autonumber
    participant You as You (edited model.go)
    participant Atlas as atlas migrate diff
    participant Loader as go run ./loader
    participant DevDB as Dev DB (ephemeral, scratch)
    participant Dir as migrations/ + atlas.sum

    You->>Atlas: atlas migrate diff add_priority --env bun
    Note over Atlas,Dir: STEP A — what does the DB look like NOW (per history)?
    Atlas->>DevDB: reset to a clean schema
    Atlas->>DevDB: replay every existing migrations/*.sql
    DevDB-->>Atlas: "current" state
    Note over Atlas,Loader: STEP B — what SHOULD it look like (per the model)?
    Atlas->>Loader: run the loader program
    Loader-->>Atlas: desired DDL for &task.Task{}
    Atlas->>DevDB: load desired schema into a clean realm
    Note over Atlas: STEP C — diff the two
    Atlas->>Atlas: compute current → desired
    Atlas->>Dir: write 2026..._add_priority.sql<br/>update atlas.sum
    Atlas-->>You: new migration file — REVIEW IT
```

The key insight: Atlas knows the "current" state by **replaying your committed
migration files** onto the throwaway dev DB, not by inspecting production. That's
why a dev DB is needed for `diff`, and why it can be empty/scratch — Atlas resets
(or recreates) it every time.

> **This is exactly why the loader-mode bug happened during the build.** In
> standalone mode, the loader emitted DDL for *every* exported struct (including
> `Handler`), so "desired" contained a phantom `handlers` table and the diff tried
> to create it. Loader mode fixes "desired" to exactly the registered models.

### Which database is which (and do you always need the dev one?)

Nothing magic marks one database as "real" and another as "throwaway" — it's the
**role you give it in `atlas.hcl`**:

- `dev = "docker://postgres/15/dev?search_path=public"` — the **dev/scratch**
  database. Atlas **resets/recreates it** on every `diff`/`lint`/`validate` to
  normalise and compute schemas, so it must be disposable, with no data you care
  about. **Never point `dev` at your app/prod database.**
- The **target** (the DB you actually change) is **not** in `atlas.hcl` by
  default; you pass it at apply time with `-u/--url` (e.g. `task migrate-apply`
  uses `-u "$DATABASE_URL"` → 5433), or — for the init container — the `deploy`
  env reads it from `DATABASE_URL` via `getenv`.

So the two are distinguished by **attribute**, not by which container: `dev = …`
is scratch; `--url`/`getenv` is the database receiving the migrations.

**Do you always need a dev DB?** Only to **generate or analyse** migrations
(`diff`, `lint`, `validate` — they build schemas in a clean DB). You do **not**
need it to **apply** — `migrate apply` only touches the target DB, which is why
the production init container ships no dev DB.

**This repo uses the ephemeral `docker://` dev DB.** `dev = "docker://postgres/15/dev"`
makes Atlas spin up a temporary Postgres per run and destroy it after — no second
container to manage. The catch: `docker://` talks to the **Docker API**, and this
host runs **podman**. A shell `alias docker=podman` does *not* help (aliases
aren't inherited by Atlas, and it uses the API, not the CLI). What works is
pointing `DOCKER_HOST` at podman's Docker-compatible socket — the Taskfile's
`migrate-diff`/`migrate-validate`/`migrate-lint` tasks set:

```
DOCKER_HOST = ${DOCKER_HOST:-unix:///run/podman/podman.sock}
```

(get yours from `podman info --format '{{.Host.RemoteSocket.Path}}'`; on a podman
machine / Windows / macOS it's a different path or pipe — set `DOCKER_HOST` in
your shell and the tasks will respect it).

**Fallback — a dedicated dev container (if `docker://` won't work for you):** no
Docker socket, a native-Windows pipe you can't reach, or you'd just rather run a
plain container. Add a second service to `compose.yaml` and point `dev` at it:

```yaml
# compose.yaml
  postgres-dev:
    image: postgres:15
    environment: { POSTGRES_USER: postgres, POSTGRES_PASSWORD: postgres, POSTGRES_DB: dev }
    ports: ["5434:5432"]
    tmpfs: ["/var/lib/postgresql/data"]   # scratch; nothing needs to survive
```

```hcl
# atlas.hcl — replace the docker:// dev line with:
dev = "postgres://postgres:postgres@localhost:5434/dev?sslmode=disable&search_path=public"
```

Then `migrate diff` needs that container up (and no `DOCKER_HOST`). SQLite users
have an even simpler option: `dev = "sqlite://file?mode=memory"`.

---

## 3. How `migrate apply` works and how Atlas tracks changes

Apply runs against a **real** database (your app DB, or production). Atlas tracks
what's been applied in a table it creates and owns: **`atlas_schema_revisions`**.

```mermaid
sequenceDiagram
    autonumber
    participant Init as init container
    participant Atlas as atlas migrate apply
    participant DB as Target Postgres
    participant Rev as atlas_schema_revisions

    Init->>Atlas: atlas migrate apply --env deploy
    Atlas->>DB: acquire advisory lock (blocks concurrent appliers)
    Atlas->>Atlas: verify atlas.sum (directory integrity)
    Atlas->>Rev: read already-applied versions<br/>(creates the table if missing)
    Atlas->>Atlas: pending = files − applied
    alt nothing pending
        Atlas-->>Init: "No migration files to execute" → exit 0
    else has pending
        loop each pending file, in version order
            Atlas->>DB: BEGIN
            Atlas->>DB: run the file's SQL statements
            Atlas->>Rev: insert row: version, description,<br/>hash, executed_at, execution_time
            Atlas->>DB: COMMIT
        end
    end
    Atlas->>DB: release lock
    Atlas-->>Init: exit 0 (success) / non-zero (failure)
```

**`atlas_schema_revisions`** is the ledger. One row per applied migration, roughly:

| column                      | meaning                                                  |
| --------------------------- | -------------------------------------------------------- |
| `version`                   | the migration's timestamp id (e.g. `20260605110617`)     |
| `description`               | the name (`add_due_date_to_tasks`)                       |
| `applied` / `total`         | statements applied vs total in the file                  |
| `executed_at`, `execution_time` | when + how long                                      |
| `hash`                      | checksum of the file as applied (tamper detection)       |
| `error` / `error_stmt`      | populated only if it failed                              |

Two safety mechanisms ride along:

- **`atlas.sum`** — a checksum manifest of the migrations *directory*. Apply
  refuses to run if a committed file was edited after the fact (history
  tampering). This is why `atlas.sum` **must be committed** and never hand-edited.
- **Advisory lock** — only one applier at a time. Critical with multiple k8s
  replicas: each pod's init container tries to apply, but they serialize; the
  first applies, the rest see "nothing pending."

---

## 4. The day-to-day: how you add a column

Concrete example — add a `priority int` to tasks.

```mermaid
flowchart TD
    S(["Need a new column: priority"]) --> E["1 Edit internal/task/model.go<br/>Priority int bun:'priority,notnull,default:0'"]
    E --> U["2 Ensure the app DB is up:  task db-up<br/>(dev DB is ephemeral — Atlas spins it per run)"]
    U --> D["3 Generate migration:<br/>atlas migrate diff add_priority_to_tasks --env bun"]
    D --> R{"4 Review the generated SQL<br/>migrations/2026..._add_priority_to_tasks.sql"}
    R -->|wrong / surprising| E
    R -->|looks right| L["5 atlas migrate validate (free, offline)<br/>+ optional lint (needs atlas login since v0.38)"]
    L --> AC["6 Expose it: add to DTOs in handler.go,<br/>Update column set in store.go"]
    AC --> T["7 task test  &&  task test-integration"]
    T --> C["8 Commit everything together (see below)"]
    C --> AP["9 Deploy → init container runs apply"]
    AP --> Z(["Schema evolved, app serves priority"])
```

### The commands

```bash
# 1. edit model.go (add the field) — then:
task db-up                                              # app DB up; Atlas spins the dev DB per run

# 2. generate — pass a NAME so the file is self-describing.
#    With the Taskfile tweak you can forward the name:
task migrate-diff -- add_priority_to_tasks
#    ...which runs: atlas migrate diff add_priority_to_tasks --env bun

# 3. review the new file, then sanity-check tests
task test
task test-integration

# 4. integrity check — free and offline:
atlas migrate validate --env bun
#    lint adds destructive/locking-change analysis but, since Atlas v0.38,
#    requires `atlas login` (free account):
atlas migrate lint --env bun --latest 1
```

### What you commit (all in one commit)

```mermaid
flowchart LR
    subgraph "git commit: 'add priority to tasks'"
        A["internal/task/model.go<br/>(the field)"]
        B["migrations/2026..._add_priority_to_tasks.sql<br/>(NEW — generated)"]
        C["migrations/atlas.sum<br/>(MODIFIED — generated)"]
        D["internal/task/handler.go, store.go<br/>(if exposing via API)"]
        E["*_test.go<br/>(coverage for the new field)"]
    end
```

The non-negotiables: **the new `.sql` file + the updated `atlas.sum`** must be
committed together with the model change. If you commit the model change but
forget the migration, the next person's `diff` will "rediscover" your change —
drift. If you commit the `.sql` but not `atlas.sum`, apply will fail the
integrity check.

---

## 5. Manual & data migrations (INSERT, seeds, custom SQL)

`atlas migrate diff` only generates **schema (DDL)** from the Bun model. For
anything it can't derive — data seeds/backfills, or raw SQL — write the migration
by hand:

```bash
# 1. create an empty, correctly-named and ordered migration file
atlas migrate new seed_default_tasks --dir "file://migrations"
#    -> migrations/<ts>_seed_default_tasks.sql  (empty)

# 2. edit it: put any SQL you want, e.g.
#    INSERT INTO "tasks" ("title","completed") VALUES ('Welcome', false);

# 3. re-hash so atlas.sum matches the edit (REQUIRED — else apply fails the
#    directory integrity check)
atlas migrate hash --dir "file://migrations"
```

Commit the new `.sql` **and** the updated `atlas.sum` together; it then applies
in version order like any other migration.

**DML is safe; manual DDL drifts** — the one trap to know:

- **Data (INSERT/UPDATE/DELETE)** — fine. It isn't schema, so the next
  `migrate diff` ignores it. (Keep the SQL valid against the schema at that point,
  since Atlas replays every file onto the dev DB when diffing.)
- **A schema object the Bun model can't express** (trigger, function, view,
  partial/expression index, CHECK constraint) — on the next `migrate diff`, Atlas
  compares the migrations-dir state (which *has* your object) against the
  model-derived desired state (which *doesn't*) and generates a migration that
  **DROPS it** as drift. Mitigate: express it on the model if you can (most plain
  indexes/uniques can be); otherwise treat it as "unmanaged" and review every
  future diff so the auto-drop never gets committed.

Useful directives at the top of a manual file:

- `-- atlas:txmode none` — run outside a transaction (e.g. `CREATE INDEX CONCURRENTLY`).
- `-- atlas:delimiter //` — custom statement delimiter (functions/triggers with internal `;`).

### Changing a migration that's created but NOT yet applied

Safe to edit freely — nothing has run it. The only thing to keep in sync is
`atlas.sum`:

| Goal | Command |
| ---- | ------- |
| Tweak the SQL by hand | edit the `.sql`, then `atlas migrate hash` — or `atlas migrate edit <version>` (opens it and re-hashes) |
| Discard it | `atlas migrate rm <version> --dir "file://migrations"` (deletes the file *and* updates `atlas.sum`) |
| Regenerate from a changed model | `atlas migrate rm` it, fix the model, re-run `atlas migrate diff <name>` |

Then sanity-check with `atlas migrate validate` (free, offline). `atlas migrate
lint` adds destructive-change analysis but requires `atlas login` since v0.38.

> **Immutability boundary:** all of the above is safe *only while the migration is
> unapplied in every shared environment*. Once it has run on staging/prod it is
> frozen — never edit it; **fix forward** with a new migration.

### Worked example — adding a foreign key by hand

The Bun provider can't emit FK constraints, so a foreign key is a **manual
migration**. This repo's `comments.task_id → tasks.id` FK was added exactly this way:

```bash
atlas migrate new add_comments_task_fk --dir "file://migrations"   # or: task migrate-new -- add_comments_task_fk
```

```sql
-- in the generated file:
ALTER TABLE "comments"
  ADD CONSTRAINT "comments_task_id_fkey"
  FOREIGN KEY ("task_id") REFERENCES "tasks" ("id") ON DELETE CASCADE;
```

```bash
atlas migrate hash --dir "file://migrations"   # task migrate-hash
task migrate-apply
```

**The drift trap:** the FK now lives in the migration history, but the
model-derived "desired" state has *no* FK — so the **next** `migrate diff`
generates `ALTER TABLE "comments" DROP CONSTRAINT "comments_task_id_fkey"` to
"reconcile" them. Commit that and your FK is gone.

**The fix** — tell Atlas never to auto-generate FK drops, via a `diff.skip` policy
in the `env` block of `atlas.hcl`:

```hcl
env "bun" {
  # ...
  diff {
    skip {
      drop_foreign_key = true
    }
  }
}
```

Now `migrate diff` reports the directory in sync and leaves the hand-managed FK
alone. **Trade-off:** Atlas will no longer auto-drop *any* FK, so removing one is
also a manual migration. The same `diff.skip` policy exists for other change
types (`drop_table`, `drop_column`, …) — the general pattern for **any** object
the Bun model can't express (triggers, functions, views, partial indexes): add it
by hand, and `skip` the matching drop so it doesn't drift.

### Merge conflicts in `atlas.sum` (two branches)

`atlas.sum` is a top line — the `h1:` hash of the **whole directory** — followed
by one hash line per file. Any added or changed migration changes both that file's
line **and** the directory hash.

So if your branch adds a migration and a teammate's branch independently adds
another, **both touched `atlas.sum`**:

- Git reports a **merge conflict** on `atlas.sum` (the single directory `h1:` line
  can't be "both"). Even if Git auto-merges the per-file lines, the directory hash
  is now wrong for the combined set.
- Atlas refuses to run against a mismatched sum — `atlas migrate validate` /
  `apply` fail with a **checksum mismatch**. That's the safety net: a half-merged
  history can't be applied.

**Fix** — keep *both* migration files, then recompute the hash:

```bash
atlas migrate hash --dir "file://migrations"   # recomputes the directory h1 + file lines
atlas migrate validate --env bun               # confirm the directory is consistent
```

If the two new migrations interleave out of order (your timestamp predates a
teammate's that already ran on a shared DB), use `atlas migrate rebase <version>`
to move yours to the end of the history and re-hash. Keeping migrations strictly
append-only (newest last) avoids "file out of order" errors at apply time.

> The directory hash changing twice is not itself a problem — it's deterministic
> from the file set, and `migrate hash` recomputes the correct value. The danger
> is a *stale* `atlas.sum` that matches neither branch; Atlas detects that and
> blocks `apply` until you re-hash.

## 6. What to do when a migration fails

Good news: **Postgres has transactional DDL**, and Atlas's default `--tx-mode
file` wraps each migration file in a transaction. So a failed statement normally
rolls back the *whole file* — the DB stays clean and you just fix-forward.

```mermaid
stateDiagram-v2
    [*] --> Clean
    Clean --> Applying: migrate apply
    Applying --> Clean: all pending succeed (exit 0)
    Applying --> Failed: a statement errors

    Failed --> RolledBack: tx-mode file + transactional DDL<br/>(the common case)
    Failed --> Dirty: non-transactional stmt<br/>(partial apply)

    RolledBack --> FixForward: fix the file / data
    FixForward --> Clean: re-run apply (resumes)

    Dirty --> Manual: inspect atlas_schema_revisions.error<br/>clean up by hand
    Manual --> Realign: atlas migrate apply (resumes pending)
    Realign --> Clean
```

**The decision that governs everything: has this migration been applied to a
*shared* environment yet?**

- **No (only failed locally / in CI, never succeeded anywhere):** you may edit the
  `.sql` file to fix it, then **re-hash**: `atlas migrate hash` (updates
  `atlas.sum`), and re-run.
- **Yes (it ran on staging/prod):** **never edit it.** Migrations are immutable
  once shared. Fix forward with a *new* migration that corrects the problem.

Practical recovery steps when an apply fails in k8s:

1. The init container exits non-zero → the pod stays in `Init:Error` and the app
   container never starts. **This is a feature** — a bad schema never gets a
   running app on top of it.
2. Inspect: `kubectl logs <pod> -c migrate`, and query
   `SELECT version, error, error_stmt FROM atlas_schema_revisions WHERE error IS NOT NULL;`
3. If it rolled back cleanly (transactional): fix forward (new migration or
   corrected unshared file), rebuild the migrate image, redeploy.
4. If partially applied (a non-transactional statement like
   `CREATE INDEX CONCURRENTLY`): you must reconcile the DB by hand, then let Atlas
   resume. Atlas refuses to proceed while the revision row shows an error until the
   state is consistent.

---

## 7. Gotchas — the stuff that bites in production

```mermaid
flowchart TD
    G1["NOT NULL without default<br/>on a non-empty table"] --> G1f["ALTER fails: existing rows have no value.<br/>FIX: add nullable (or with default) → backfill → set NOT NULL later"]
    G2["Destructive change during<br/>rolling deploy (drop/rename column)"] --> G2f["Old app pods still run vs new schema during rollout → 500s.<br/>FIX: expand/contract (additive now, remove a release later)"]
    G3["Rename a column"] --> G3f["Atlas often sees drop+add = DATA LOSS.<br/>FIX: review the SQL; hand-edit to ALTER...RENAME, then atlas migrate hash"]
    G4["Two branches each add a migration"] --> G4f["atlas.sum merge conflict + version ordering.<br/>FIX: regenerate/rehash after merge; keep versions ordered"]
    G5["Edited an already-applied migration"] --> G5f["atlas.sum mismatch → apply blocked.<br/>RULE: shared migrations are immutable. Fix forward."]
    G6["Loader registers extra exported structs"] --> G6f["Phantom tables in the diff (this project hit it).<br/>RULE: loader/main.go loads exactly the models."]
    G7["Adopting Atlas on an EXISTING db"] --> G7f["apply tries to re-create existing tables.<br/>FIX: baseline — atlas migrate apply --baseline <version>"]
```

A few expanded, because they matter most for the k8s setup:

### ① Rolling deploys need backward-compatible schema (expand/contract)

The init container applies the new schema *before* the new app pods are fully
rolled out — but during a rolling update, **old app pods are still serving against
the new schema**. If a migration drops or renames a column the old code still
selects, those pods throw errors until they're replaced.

```mermaid
sequenceDiagram
    participant Old as Old app pods (still running)
    participant Init as init container (new release)
    participant DB as Postgres
    participant New as New app pods

    Init->>DB: apply migration (e.g. DROP COLUMN x)
    Note over Old,DB: ⚠️ Old pods still SELECT x → errors during rollout window
    Init->>New: rollout proceeds
    New->>DB: queries match new schema ✓
```

The safe pattern is **expand → migrate → contract across two releases**:

- Release 1 (expand): add the new column (nullable/additive). Deploy code that
  writes both old + new. Backfill.
- Release 2 (contract): stop using the old column. Drop it.

Adding a nullable column (like `due_date`) is already safe — it's purely additive.
Dropping/renaming is where you need the two-step dance.

### ② `NOT NULL` on an existing table

`ALTER TABLE tasks ADD COLUMN priority int NOT NULL` fails if rows exist. Either
give it `default:0` (the bun tag can: `bun:"priority,notnull,default:0"` → Atlas
emits `DEFAULT 0`), or add nullable, backfill, then tighten.

### ③ Renames look like data loss

If you rename `title` → `name` in the struct, Atlas diff typically generates
`DROP COLUMN title; ADD COLUMN name` — losing data. Always **review** the
generated SQL; for a true rename, hand-edit to `ALTER TABLE ... RENAME COLUMN`,
then run `atlas migrate hash` so `atlas.sum` matches.

### ④ Always review; validate (and lint)

`atlas migrate diff` is mechanical; it doesn't know intent — always read the
generated SQL. `atlas migrate validate` checks directory integrity offline and
for free. `atlas migrate lint --env bun --latest 1` adds destructive/irreversible/
locking-change analysis, but **since Atlas v0.38 it requires `atlas login`** (free
account) — so it's a great CI gate but no longer a purely-offline command.

### ⑤ `updated_at` is app-set, not DB-set here

The handler sets `updated_at` in Go on update; there's no DB trigger. So
migrations won't add one — if you ever want DB-enforced `updated_at`, that's a
deliberate migration (a trigger), not something Atlas derives from the struct.

---

## 8. Cheat sheet

```bash
# --- inspect ---
task migrate-status                         # what's applied to the app DB
atlas migrate status --env bun -u "$DATABASE_URL"

# --- evolve the schema (dev time) ---
task db-up                                  # app DB; Atlas spins an ephemeral dev DB per diff
task migrate-diff -- <descriptive_name>     # atlas migrate diff <name> --env bun
task migrate-validate                       # integrity check (free, offline)
task migrate-lint                           # destructive-change analysis (needs `atlas login` since v0.38)
task migrate-hash                           # ONLY after hand-editing an unshared migration

# --- manual / data migration (INSERT, custom SQL, foreign keys) ---
task migrate-new -- <name>                  # empty file to fill in, then `task migrate-hash`

# --- change an UNAPPLIED migration ---
atlas migrate edit <version>                # edit + re-hash in one step
atlas migrate rm   <version> --dir "file://migrations"   # discard it + re-hash

# --- apply (locally) ---
task migrate-apply                          # → app DB on 5433

# --- apply (production) = the init container does this automatically ---
#   atlas migrate apply --env deploy        (reads DATABASE_URL via getenv)

# --- adopting Atlas on a pre-existing DB ---
atlas migrate apply --env deploy --baseline <existing_version>
```

**The golden rules:**

1. Model is the source of truth → never hand-write migrations.
2. Commit the `.sql` **and** `atlas.sum` with the model change, atomically.
3. Shared migrations are immutable → fix forward, never edit.
4. Review (and lint) every generated migration before committing.
5. Additive changes are safe; destructive changes need expand/contract across releases.
