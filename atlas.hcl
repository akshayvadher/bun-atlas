data "external_schema" "bun" {
  # Program mode: ./loader registers exactly the Task model and prints its DDL.
  # We use program mode instead of standalone `load --path ./internal/task`
  # because the standalone scanner (atlas-provider-bun v0.0.3) treats EVERY
  # exported struct in the package as a table — it would emit a phantom
  # "handlers" table for the exported Handler type. Listing the model
  # explicitly in ./loader keeps the diff limited to the real source of truth.
  program = [
    "go", "run", "-mod=mod",
    "./loader",
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
