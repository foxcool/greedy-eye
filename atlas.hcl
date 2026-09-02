variable "database_url" {
  type    = string
  default = getenv("EYE_DB_URL")
}

# schema.hcl is the authoring surface; migrations/ is the upgrade path.
#
# Nobody hand-writes the SQL: a schema change is made in schema.hcl and turned
# into an ordered file by `make migrate-diff name=<what_changed>`. An instance
# then runs `atlas migrate apply`, which executes only the files it has not run
# yet and records them in atlas_schema_revisions. This is what makes an upgrade
# possible without a human reading a diff — and what stops a stale checkout from
# dropping a column, since a migration only ever contains what somebody wrote
# into it.
#
# The two surfaces are kept in step by TestMigrationsMatchSchema, not by
# discipline: it replays the directory into an empty database and fails if the
# result differs from schema.hcl.
env "local" {
  src = "file://schema.hcl"
  url = var.database_url
  dev = "docker://postgres/17/greedy_eye?search_path=public"

  migration {
    dir = "file://migrations"
  }
}

env "docker" {
  src = "file://schema.hcl"
  url = var.database_url
  dev = "docker://postgres/17/greedy_eye?search_path=public"

  migration {
    dir = "file://migrations"
  }
}

env "test" {
  src = "file://schema.hcl"
  url = var.database_url

  migration {
    dir = "file://migrations"
  }
}
