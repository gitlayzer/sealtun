.PHONY: build clean fmt tidy test help

# Go binary
GO ?= go

# Binary name
BINARY_NAME=sealtun

# Get version from git
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")

# Build flags
LDFLAGS=-ldflags "-s -w -X github.com/labring/sealtun/pkg/version.Version=$(VERSION)"

## build: build the binary
build:
	$(GO) build $(LDFLAGS) -o $(BINARY_NAME) main.go

## clean: clean the binary
clean:
	rm -f $(BINARY_NAME)

## fmt: format the code
fmt:
	go fmt ./...

## tidy: tidy the go mod
tidy:
	go mod tidy

## help: show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^##' Makefile | sed 's/## //g' | awk -F ':' '{printf "  %-12s %s\n", $$1, $$2}'
