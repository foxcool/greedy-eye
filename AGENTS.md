# Greedy Eye

Personal investment intelligence platform: a Go modular monolith serving
Connect-RPC, PostgreSQL via pgx, schema managed by Atlas.

## How We Work

- Run `make check` before every commit (vet, lint, unit tests).
- Run the checks through make targets, not host tools. `make lint` runs
  golangci-lint in Docker at the version CI pins, so it matches CI; `gofmt`,
  the build-tag check, `go vet` and the unit tests run on the host toolchain
  through the same targets, as they do on CI runners.
- `make check` does not include the static security scanner (gosec). Run
  `make gosec` before a push: it runs the scanner in Docker at the version CI
  pins, with CI's arguments (CI calls the same target in
  `.github/workflows/security.yml`).
- `make up` starts the dev stand and its `migrate` service applies pending
  migrations first. A stand started from a branch therefore changes the dev
  database, including for migrations that branch has not merged.
- Commits are signed. If signing fails, retry; never bypass it with
  `--no-gpg-sign` or by disabling `commit.gpgsign`.
- Branch prefixes: `feature/`, `bugfix/`, `hotfix/`. Commit messages are short
  and follow the history (`feat(scope): ...`, `fix(scope): ...`).
- A PR description has two sections: **How verified** (commands run and their
  result, what was not run and why, what a human must check on the stand) and
  **Found debt** (what was noticed and not fixed: where, what is claimed vs
  what happens, what done looks like).
- Do not fix unrelated problems in passing; write them under Found debt.
- Tasks are tracked outside this repository. Nothing here describes the
  tracker, its commands or its paths.
- No tool brand names and no tracker ids in code, comments or commit messages.
- The code wins over the docs: if they disagree, fix the doc or report it.

## Documentation Map

| File | Question it answers |
|---|---|
| `README.md` | What is this project, what does it do, how do I start it? |
| `docs/architecture.md` | How is the system built, and why (arc42 sections, ADRs, risks and debt)? |
| `docs/development.md` | How do I set up, configure and work on it locally? |
| `docs/providers.md` | Which external providers can be connected and what does each need? |
| `docs/logo.svg` | The logo image used by the README (not prose). |
| `site/` | The project's public landing page: static HTML and CSS, no build step, published to https://foxcool.github.io/greedy-eye/ by `.github/workflows/pages.yml` on every push to main that touches it. Its claims follow README; screenshots come from the frontend's `make screenshots` (demo data). |

Also: `api/v1/*.proto` is the API contract, `schema.hcl` the schema,
`cmd/eye/main.go` the wiring.

## Project Structure

- `cmd/eye/` - Binary: wiring, config, provider registration
- `internal/adapter/` - External API clients; `ratelimit/` holds the shared
  rate and quota budget per credential
- `internal/entity/` - Domain entities and provider interfaces
- `internal/middleware/` - User provisioning, ownership, service identity
- `internal/service/` - Connect handlers and business logic
- `internal/spend/` - Spend class and caller labels carried in a context
- `internal/store/postgres/` - PostgreSQL data layer (pgx)
- `api/v1/` - Protocol Buffer definitions

## Commands

```bash
make check            # vet + lint (Docker) + unit tests: run before commit
make lint             # gofmt, build-tag check, golangci-lint in Docker
make gosec            # gosec in Docker, CI's arguments; not part of check
make vet              # go vet
make test-unit        # Unit tests (race detector, coverage)
make test-integration # Integration tests (testcontainers, needs Atlas CLI)
make test             # Unit + integration
make test-smoke       # Smoke suite on its own stand, never on the dev stand
make test-smoke-loopback # Same suite, with the stand calling itself over the wire
make buf-gen          # Regenerate protobuf code
make up               # Dev stand; applies pending migrations (see above)
make down             # Stop and remove the stand's containers
```

Database commands are listed under Critical Rules.

## Critical Rules

### Database

schema.hcl is the authoring surface; `migrations/` is what instances run.
Never hand-write a migration and never apply schema.hcl to a database:

```bash
make migrate-diff name=what_changed  # schema.hcl edit -> ordered migration file
make migrate-apply                   # run what this database has not run
make migrate-status                  # executed vs pending
make migrate-drift                   # what a live database has that schema.hcl does not
```

`TestMigrationsMatchSchema` (integration) fails when the two surfaces disagree,
so a schema.hcl change without a migration cannot reach main.

### Trust headers

`X-User-*`, `X-Service-Token`, `X-Sync-Authority` and `X-Spend-Class` grant rights
(`internal/middleware`); `X-Spend-Caller` only names who is charged, but is trusted and
stripped the same way.
An interceptor that writes them deletes them first and sets them only from ctx, so a
reused or copied request cannot carry a previous caller's rights to the next hop.

### Never

- Run smoke tests against the dev stand: the suite truncates every table
  (`make test-smoke` uses its own database).
- Commit secrets, `.env` files or provider keys. Provider credentials are
  accounts in the database, not environment variables.
- Bypass commit signing or hooks.
