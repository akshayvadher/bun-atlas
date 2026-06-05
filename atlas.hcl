data "external_schema" "bun" {
  # Program mode: ./loader registers every Bun model (Task, Comment) and prints
  # their DDL. We use program mode instead of standalone `load --path` because
  # the standalone scanner treats EVERY exported struct in a package as a table
  # (it would emit phantom tables for the Handler types). Listing models
  # explicitly in ./loader keeps the diff limited to the real sources of truth.
  program = [
    "go", "run", "-mod=mod",
    "./loader",
  ]
}

env "bun" {
  src = data.external_schema.bun.url
  # Dev database: a throwaway Postgres Atlas resets to compute diffs. We use the
  # EPHEMERAL docker:// driver — Atlas spins a temporary container per run and
  # destroys it afterwards. It talks to the Docker *API* (not the `docker` CLI),
  # so with podman you set DOCKER_HOST to podman's Docker-compatible socket; the
  # Taskfile does this for the diff/validate/lint tasks.
  #
  # Stuck (no socket / native-Windows pipe / using real Docker)? See "Which
  # database is which" in docs/migrations-guide.md for the dedicated-container
  # fallback (a second compose service on a fixed port).
  dev = "docker://postgres/15/dev?search_path=public"
  migration {
    dir = "file://migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
  # The Bun provider can't express foreign keys, so the comments.task_id FK is
  # added by a MANUAL migration (20260605140922_add_comments_task_fk.sql). Without
  # this policy, every subsequent `migrate diff` would see the FK as drift (it's
  # in the migration history but not in the model-derived desired state) and try
  # to DROP it. Skipping drop_foreign_key tells Atlas not to auto-generate FK
  # drops — so the hand-managed FK survives. Trade-off: removing an FK is then
  # also a manual migration.
  diff {
    skip {
      drop_foreign_key = true
    }
  }
}
