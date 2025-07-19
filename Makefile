# Define makefile variables for frequently used commands
BUF=$(shell which buf)
# Use docker compose instead of docker-compose
COMPOSE=docker compose -p eye
# Path to the compose file
COMPOSE_FILE=deploy/compose.yaml

.PHONY: gen go-gen migrations migrate-apply up debug down logs clean buf-gen docs-api

# Generate all code
gen: buf-gen go-gen migrations

# Generate all files from .proto sources using buf
buf-gen:
ifndef BUF
	@echo "Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@latest
endif
	@echo "Generating protobuf files with buf..."
	mkdir -p docs/api
	buf generate --template buf.gen.yaml
	@echo "Renaming OpenAPI spec to standard name..."
	@if [ -f docs/api/openapi.swagger.yaml ]; then mv docs/api/openapi.swagger.yaml docs/api/openapi.yaml; fi
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

# Generate migrations
migrations:
	@echo "📝 Generating diff against ephemeral DB…"
	atlas migrate diff \
	   -c file://deploy/migrations/atlas.hcl --env docker

migrate-apply:
	@echo "Applying migrations using compose run..."
	$(COMPOSE) -f $(COMPOSE_FILE) run --rm atlas-cli migrate apply \
		--config "file:///greedy-eye/deploy/migrations/atlas.hcl" \
		--dir "file:///greedy-eye/deploy/migrations" \
		--env docker

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

# Run tests in a dedicated test container
test:
	@echo "Running tests in a dedicated test container..."
	$(COMPOSE) -f $(COMPOSE_FILE) --env-file deploy/secrets.env run --rm \
	-e DOCKER_COMPOSE_TEST=true \
	eye-test go test ./... -tags=integration
