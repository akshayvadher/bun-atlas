data "external_schema" "bun" {
  # Standalone mode: the provider scans ./internal/task for Bun models and
  # prints their DDL. (Switched to program/loader mode in a later slice.)
  program = [
    "go", "run", "-mod=mod",
    "ariga.io/atlas-provider-bun",
    "load",
    "--path", "./internal/task",
    "--dialect", "postgres",
  ]
}

env "bun" {
  src = data.external_schema.bun.url
  # Dev database: a throwaway Postgres Atlas resets to compute diffs. We point
  # at a real container (compose.yaml service "postgres-dev" on host port 5434)
  # rather than "docker://..." because this host has podman only, no docker CLI.
  dev = "postgres://postgres:postgres@localhost:5434/dev?sslmode=disable&search_path=public"
  migration {
    dir = "file://migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
