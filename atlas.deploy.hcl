# Apply-only Atlas config for the migration image / init container.
#
# Unlike the project-root atlas.hcl (which wires the Bun loader + a dev
# database to GENERATE migrations via `migrate diff`), this config only knows
# how to APPLY the already-generated migrations to a real database. It needs
# no Go toolchain and no dev DB — keeping the migration image tiny.
#
# The database URL is read from the DATABASE_URL environment variable at
# runtime, so the same image works under `docker run -e DATABASE_URL=...` and
# as a Kubernetes init container with DATABASE_URL set from a Secret.
env "deploy" {
  url = getenv("DATABASE_URL")

  migration {
    dir = "file:///migrations"
  }
}
