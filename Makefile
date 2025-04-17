# Define makefile variables for frequently used commands
PROTOC_GEN_GO=$(shell which protoc-gen-go)
# Use docker compose instead of docker-compose
COMPOSE=docker compose -p eye
# Path to the compose file
COMPOSE_FILE=deploy/compose.yaml

.PHONY: gen protoc go-gen migrations migrate-apply up debug down logs clean

# Generate all code
gen: protoc go-gen migrations

# Generate Go files from .proto sources
protoc:
ifndef PROTOC_GEN_GO
	@echo "Installing protoc-gen-go with grpc plugin..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
endif
	@echo "Generating .proto files..."
	protoc --go_out=internal/ --go_opt=paths=source_relative \
		--go-grpc_out=internal/ --go-grpc_opt=paths=source_relative \
	$(shell find api -name "*.proto")
	@echo "Protobuf files generated in internal/"

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
