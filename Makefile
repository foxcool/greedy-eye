# Define makefile variables for frequently used commands
BUF=$(shell which buf)
# Use docker compose instead of docker-compose
COMPOSE=docker compose -p eye
# Path to the compose file
COMPOSE_FILE=deploy/compose.yaml

.PHONY: gen go-gen up debug down logs clean buf-gen docs-api \
        test test-unit test-integration schema-apply schema-diff

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

# Run unit tests only
test-unit:
	@echo "Running unit tests..."
	go test -v -race ./...

# Run integration tests (requires Atlas CLI and Docker)
test-integration:
	@which atlas > /dev/null || (echo "Atlas CLI required: curl -sSf https://atlasgo.sh | sh" && exit 1)
	@echo "Running integration tests..."
	go test -v -p 1 -tags=integration ./internal/store/postgres/...

# Atlas: apply schema to dev database
schema-apply:
	@which atlas > /dev/null || (echo "Atlas CLI required: curl -sSf https://atlasgo.sh | sh" && exit 1)
	atlas schema apply --env local --auto-approve

# Atlas: show schema diff
schema-diff:
	@which atlas > /dev/null || (echo "Atlas CLI required: curl -sSf https://atlasgo.sh | sh" && exit 1)
	atlas schema diff --env local

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
