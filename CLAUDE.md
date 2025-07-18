# Greedy Eye - Context for Claude

  ## Project Overview

  Comprehensive portfolio management system for cryptocurrency investments built in Go.

  ## Current Architecture

  - Modular monolith with gRPC services + HTTP API Gateway
  - PostgreSQL database with Ent ORM
  - gRPC-Gateway for HTTP API auto-generation
  - Dual-server architecture (gRPC:50051 + HTTP:8080)

  ## Key Files

  - `docs/architecture/overview.md` - Architecture overview
  - `docs/features/grpc-gateway.md` - Current HTTP API implementation
  - `docs/development/progress.md` - Development progress
  - `cmd/eye/main.go` - Main application entry point

  ## Current Status

  - Phase 1: gRPC-Gateway foundation ✅ COMPLETED
  - Phase 2: Security + Rule Services 🔄 IN PROGRESS
  - Phase 3: Integration Services + Testing 📋 PLANNED

  ## Common Tasks

  - `make buf-gen` - Generate protobuf code
  - `make dev` - Run with live reload
  - `make test` - Run tests
  - `docker-compose up -d postgres` - Start database

  2. Структурированные заметки по разработке

  Создайте файл docs/development/notes.md для быстрых заметок:

  ## Development Notes

  ### Current Context

  - Working on HTTP API Gateway implementation
  - Phase 1 complete: gRPC-Gateway foundation
  - Next: AuthService and RuleService implementation

  ### Quick References

  - gRPC port: 50051
  - HTTP port: 8080
  - Health check: http://localhost:8080/health
  - Database: PostgreSQL on port 5432

  ### Common Issues

  - Hardcoded HTTP port fixed
  - HTTP annotations completed for all services
  - Error handling strategy implemented

