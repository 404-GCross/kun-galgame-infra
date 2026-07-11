.PHONY: build run migrate migrate-drop test clean

# Build variables
BUILD_DIR=./build

# Go variables
GO=go
GOFLAGS=-v

# Build all services
build:
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/oauth ./cmd/oauth
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/artifact ./cmd/artifact
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/trust ./cmd/trust
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/worker ./cmd/worker
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/migrate ./cmd/migrate

# Run individual services
run-oauth:
	$(GO) run ./cmd/oauth

run-artifact:
	$(GO) run ./cmd/artifact

run-trust:
	$(GO) run ./cmd/trust

run-worker:
	$(GO) run ./cmd/worker

# Run database migrations
migrate:
	$(GO) run ./cmd/migrate

# Run migrations with drop (DANGEROUS - development only)
migrate-drop:
	$(GO) run ./cmd/migrate -drop

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
	rm -rf tmp
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

# Development: run OAuth service with hot reload (requires air)
dev:
	air

# Help
help:
	@echo "Available targets:"
	@echo "  build              - Build all service binaries"
	@echo "  run-oauth          - Run the OAuth service"
	@echo "  run-artifact       - Run the Artifact service"
	@echo "  run-trust          - Run the Trust & Safety service"
	@echo "  run-worker         - Run the worker"
	@echo "  migrate            - Run database migrations"
	@echo "  migrate-drop       - Drop all tables and run migrations (DANGEROUS)"
	@echo "  test               - Run tests"
	@echo "  test-coverage      - Run tests with coverage report"
	@echo "  clean              - Clean build artifacts"
	@echo "  fmt                - Format code"
	@echo "  lint               - Run linter"
	@echo "  tidy               - Tidy dependencies"
	@echo "  dev                - Run OAuth service with hot reload (requires air)"
