# Define makefile variables for frequently used commands
BUF=$(shell which buf)
# Use docker compose instead of docker-compose
COMPOSE=docker compose -p eye --env-file deploy/.env
# Path to the compose file
COMPOSE_FILE=deploy/compose.yaml

.PHONY: gen go-gen up debug down logs clean buf-gen docs-api \
        test test-unit test-integration test-smoke schema-diff \
        migrate-apply migrate-diff migrate-status migrate-baseline migrate-drift \
        lint vet check

# Generate all code
gen: buf-gen go-gen

# Generate all files from .proto sources using buf
buf-gen:
ifndef BUF
	@echo "Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@latest
endif
	@echo "Generating protobuf files with buf..."
	buf generate --template buf.gen.yaml
	@echo "Renaming OpenAPI spec to standard name..."
	@if [ -f docs/openapi.swagger.yaml ]; then mv docs/openapi.swagger.yaml docs/openapi.yaml; fi
	@echo "Protobuf files generated"

# Generate OpenAPI documentation
docs-api: buf-gen
	@echo "OpenAPI documentation generated in docs/api/"

# Legacy protoc command (deprecated, use buf-gen instead)
protoc: buf-gen

# Generate go code
go-gen:
	@echo "Generating go code..."
	go generate ./...

# Run all tests
test: test-unit test-integration

# Run unit tests only (matches CI: race detector + coverage)
test-unit:
	@echo "Running unit tests..."
	go test -v -race -coverprofile=coverage-unit.out -covermode=atomic ./...

# Run integration tests (requires Atlas CLI and Docker)
test-integration:
	@which atlas > /dev/null || (echo "Atlas CLI required: curl -sSf https://atlasgo.sh | sh" && exit 1)
	@echo "Running integration tests..."
	go test -v -p 1 -tags=integration ./internal/store/postgres/...

# Run smoke tests against the live compose stack.
# Starts eye-dev (and its deps postgres + migrate) automatically via depends_on.
# API keys come from deploy/secrets.env. Binance always runs; CoinGecko/Moralis need keys.
test-smoke:
	$(COMPOSE) -f $(COMPOSE_FILE) --profile default --profile test run --rm \
		eye-test go test -v -p 1 -tags=smoke -timeout 120s ./test/smoke/...

# Atlas targets all run inside the compose migrate service, against the compose
# postgres — same image and same commands an instance runs on deploy.
ATLAS=$(COMPOSE) -f $(COMPOSE_FILE) run --rm migrate
DB_URL="postgres://greedy_eye:password@postgres:5432/greedy_eye?sslmode=disable"
# The same database, scoped to the schema the application owns. Atlas keeps its
# bookkeeping in a separate "atlas" schema, so a diff must say which schema it
# is about or it reads the bookkeeping as drift.
DB_URL_PUBLIC="postgres://greedy_eye:password@postgres:5432/greedy_eye?sslmode=disable&search_path=public"
# Revisions live outside the application schema: what schema.hcl describes and
# what the database holds stay comparable.
REVISIONS=--revisions-schema atlas
# Atlas needs a scratch database to materialise schema.hcl into; the postgres
# healthcheck creates it.
ATLAS_DEV_URL="postgres://greedy_eye:password@postgres:5432/atlas_dev?sslmode=disable"

# Atlas: run the migrations this database has not run yet.
migrate-apply:
	$(ATLAS) migrate apply --url $(DB_URL) --dir "file:///migrations" $(REVISIONS)

# Atlas: turn a schema.hcl edit into a migration file.
# Usage: make migrate-diff name=holdings_excluded_source
migrate-diff:
	@test -n "$(name)" || (echo "usage: make migrate-diff name=<snake_case_what_changed>" && exit 1)
	$(ATLAS) migrate diff $(name) \
		--dir "file:///migrations" \
		--to "file:///schema.hcl" \
		--dev-url $(ATLAS_DEV_URL)

# Atlas: which migrations this database has run, and what is pending.
migrate-status:
	$(ATLAS) migrate status --url $(DB_URL) --dir "file:///migrations" $(REVISIONS)

# Atlas: mark a database that already carries the schema as being at <version>,
# so the next apply starts from there instead of trying to recreate everything.
# Run ONCE per pre-existing instance, and only after `make migrate-drift` says
# the database matches that version. Usage: make migrate-baseline version=20260902112400
migrate-baseline:
	@test -n "$(version)" || (echo "usage: make migrate-baseline version=<migration version>" && exit 1)
	$(ATLAS) migrate apply --url $(DB_URL) --dir "file:///migrations" $(REVISIONS) --baseline $(version)

# Atlas: what a database has that the desired schema does not, and vice versa.
# Read-only. This is the check to run before baselining an instance, and the
# way to find out whether an instance drifted.
migrate-drift:
	$(ATLAS) schema diff \
		--from $(DB_URL_PUBLIC) \
		--to "file:///schema.hcl" \
		--dev-url $(ATLAS_DEV_URL)

# Kept as an alias: the old name, now meaning the read-only check.
schema-diff: migrate-drift

# Run go vet (fast static analysis)
vet:
	go vet ./...

# Run golangci-lint via Docker (matches CI)
lint:
	docker run --rm -v $(PWD):/app -w /app golangci/golangci-lint:latest golangci-lint run -v

# Pre-commit check: vet + lint + unit tests
check: vet lint test-unit

# Run default/development profile services in detached mode
up:
	@echo "Starting Docker Compose (default profile)..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile default up --build -d --remove-orphans

# Run debug profile services in detached mode
debug:
	@echo "Starting Docker Compose (debug profile)..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile debug up --build -d --remove-orphans

stop:
	@echo "Stopping services..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile dev stop

# Stop and remove containers, networks, volumes defined in compose
down: stop
	$(COMPOSE) -f $(COMPOSE_FILE) down --remove-orphans

# Stop and remove containers, networks, AND remove volumes (use with caution!)
clean: down
	@echo "Cleaning up Docker Compose (removing volumes)..."
	$(COMPOSE) -f $(COMPOSE_FILE) down -v --remove-orphans

# Follow logs for a eye service
logs:
	@echo "Following logs for eye_prod service..."
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f eye-dev

# Follow logs for the debug service
logs-debug:
	@echo "Following logs for eye_debug service..."
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f eye-debug
