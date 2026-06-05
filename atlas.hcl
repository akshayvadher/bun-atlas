data "external_schema" "bun" {
  # Standalone mode: the provider scans ./internal/task for Bun models and
  # prints their DDL. (Switched to program/loader mode in a later slice — see
  # the loader/ directory — because the standalone scanner treats every exported
  # struct as a table.)
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
  # Dev database Atlas resets to compute diffs. docker://... spins one up via the
  # Docker CLI.
  dev = "docker://postgres/15/dev?search_path=public"
  migration {
    dir = "file://migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
