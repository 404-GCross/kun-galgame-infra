.PHONY: build run migrate migrate-drop migrate-users test clean

# Build variables
BINARY_NAME=kun-oauth-admin
BUILD_DIR=./build

# Go variables
GO=go
GOFLAGS=-v

# Build the server
build:
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/server ./cmd/server
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/worker ./cmd/worker
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/migrate ./cmd/migrate
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/migrate-users ./cmd/migrate-users

# Run the server
run:
	$(GO) run ./cmd/server

# Run the worker
run-worker:
	$(GO) run ./cmd/worker

# Run database migrations
migrate:
	$(GO) run ./cmd/migrate

# Run migrations with drop (DANGEROUS - development only)
migrate-drop:
	$(GO) run ./cmd/migrate -drop

# Migrate users from kungal and moyu databases
# Usage: make migrate-users KUNGAL_DSN="host=localhost..." MOYU_DSN="host=localhost..."
migrate-users:
	$(GO) run ./cmd/migrate-users --kungal-dsn="$(KUNGAL_DSN)" --moyu-dsn="$(MOYU_DSN)"

# Dry run user migration
migrate-users-dry:
	$(GO) run ./cmd/migrate-users --dry-run --kungal-dsn="$(KUNGAL_DSN)" --moyu-dsn="$(MOYU_DSN)"

# Run tests
test:
	$(GO) test -v ./...

# Run tests with coverage
test-coverage:
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Format code
fmt:
	$(GO) fmt ./...

# Run linter
lint:
	golangci-lint run

# Tidy dependencies
tidy:
	$(GO) mod tidy

# Generate (if using go generate)
generate:
	$(GO) generate ./...

# Development: run with hot reload (requires air)
dev:
	air

# Help
help:
	@echo "Available targets:"
	@echo "  build            - Build all binaries"
	@echo "  run              - Run the server"
	@echo "  run-worker       - Run the worker"
	@echo "  migrate          - Run database migrations"
	@echo "  migrate-drop     - Drop all tables and run migrations (DANGEROUS)"
	@echo "  migrate-users    - Migrate users from kungal/moyu databases"
	@echo "  migrate-users-dry- Dry run user migration"
	@echo "  test             - Run tests"
	@echo "  test-coverage    - Run tests with coverage report"
	@echo "  clean            - Clean build artifacts"
	@echo "  fmt              - Format code"
	@echo "  lint             - Run linter"
	@echo "  tidy             - Tidy dependencies"
	@echo "  dev              - Run with hot reload (requires air)"
	@echo ""
	@echo "User migration example:"
	@echo "  make migrate-users KUNGAL_DSN=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame sslmode=disable\" MOYU_DSN=\"host=localhost port=5432 user=postgres password=xxx dbname=kungalgame_patch sslmode=disable\""
