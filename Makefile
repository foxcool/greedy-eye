# Define makefile variables for frequently used commands
PROTOC_GEN_GO=$(shell which protoc-gen-go)
DOCKER_COMPOSE_FILES=deploy/docker-compose.yml

.PHONY: protoc

# Generate Go files from .proto sources
protoc:
ifndef PROTOC_GEN_GO
	@echo "Installing protoc-gen-go..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
endif
	@echo "Generating .proto files..."
	protoc --go_out=pkg/api --go-grpc_out=pkg/api $(shell find api -name "*.proto")

# Run docker-compose up
dev:
	@echo "Starting Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILES) up