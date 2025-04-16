# Define makefile variables for frequently used commands
PROTOC_GEN_GO=$(shell which protoc-gen-go)
# Use docker compose instead of docker-compose
COMPOSE=docker compose -p eye
# Path to the compose file
COMPOSE_FILE=deploy/docker-compose.yml

.PHONY: all protoc generate up debug down logs clean analytics

# Generate all code
all: protoc generate

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

# Generate other code
generate:
	@echo "Generating code..."
	go generate ./...

# Run default/development profile services in detached mode
up:
	@echo "Starting Docker Compose (default profile)..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile default up --build -d --remove-orphans

# Run debug profile services in detached mode
debug:
	@echo "Starting Docker Compose (debug profile)..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile debug up --build -d --remove-orphans

# Run analytics profile services (Redash) in detached mode
analytics:
	@echo "Starting Docker Compose (analytics profile)..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile analytics up --build -d --remove-orphans

analytics-db:
	@echo "Run redash create_db..."
	$(COMPOSE) -f $(COMPOSE_FILE) run --rm redash create_db

stop:
	@echo "Stopping services..."
	$(COMPOSE) -f $(COMPOSE_FILE) --profile analytics stop
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
