.PHONY: build clean test lint

BINARY_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT)"

build:
	@mkdir -p $(BINARY_DIR)
	@echo "Building fab-pr-pipeline..."
	@go build $(LDFLAGS) -o $(BINARY_DIR)/fab-pr-pipeline .

clean:
	@rm -rf $(BINARY_DIR)

test:
	@go test ./...

lint:
	@golangci-lint run ./...

fmt:
	@gofmt -s -w .

check: lint test
	@echo "All checks passed."
