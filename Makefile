.PHONY: build clean fmt tidy test npm-packages npm-pack npm-publish npm-publish-dry-run npm-clean help

# Go binary
GO ?= go
NODE ?= node
NPM ?= npm

# Binary name
BINARY_NAME=sealtun

# Development builds use the explicit dev channel; releases inject semver via GoReleaser.
VERSION ?= dev
NPM_LATEST_RELEASE_TAG ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
NPM_VERSION ?= $(shell echo $(NPM_LATEST_RELEASE_TAG) | sed 's/^v//')
NPM_RELEASE_TAG ?= v$(NPM_VERSION)
NPM_GITHUB_REPO ?= gitlayzer/sealtun
NPM_PACKAGE_NAME ?= sealtun
NPM_BINARY_PACKAGE_SCOPE ?= @gitlayzer
NPM_PACKAGES_DIR ?= packages
NPM_DIST_TAG ?= latest
NPM_PUBLISH_FLAGS ?= --access public
NPM_DRY_RUN_VERSION ?= 0.0.0-dry-run.$(shell date +%Y%m%d%H%M%S)
NPM_DRY_RUN_RELEASE_TAG ?= $(NPM_LATEST_RELEASE_TAG)
NPM_SKIP_EXISTING ?= 0
NPM_PUBLISH_RETRIES ?= 3
NPM_PUBLISH_REPORT ?= $(NPM_PACKAGES_DIR)/publish-report.json

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

## test: run tests
test:
	$(GO) test ./...

## npm-packages: generate npm packages from GitHub Release assets
npm-packages:
	$(NODE) scripts/build-npm-packages.mjs \
		--repo $(NPM_GITHUB_REPO) \
		--tag $(NPM_RELEASE_TAG) \
		--version $(NPM_VERSION) \
		--package-name $(NPM_PACKAGE_NAME) \
		--binary-package-scope $(NPM_BINARY_PACKAGE_SCOPE) \
		--out-dir $(NPM_PACKAGES_DIR)

## npm-pack: generate npm packages and create local npm tarballs
npm-pack: npm-packages
	@set -eu; \
	for pkg in $(NPM_PACKAGES_DIR)/*; do \
		if [ -f "$$pkg/package.json" ]; then \
			echo "Packing $$pkg"; \
			(cd "$$pkg" && $(NPM) pack); \
		fi; \
	done; \
	echo "Packing $(NPM_PACKAGES_DIR)"; \
	(cd "$(NPM_PACKAGES_DIR)" && $(NPM) pack)

## npm-publish: publish platform packages first, then the main npm package
npm-publish: npm-packages
	$(NODE) scripts/publish-npm-packages.mjs \
		--out-dir $(NPM_PACKAGES_DIR) \
		--dist-tag $(NPM_DIST_TAG) \
		--skip-existing $(NPM_SKIP_EXISTING) \
		--retries $(NPM_PUBLISH_RETRIES) \
		--report $(NPM_PUBLISH_REPORT) \
		-- $(NPM_PUBLISH_FLAGS)

## npm-publish-dry-run: verify the npm publish payload without publishing
npm-publish-dry-run: NPM_VERSION := $(NPM_DRY_RUN_VERSION)
npm-publish-dry-run: NPM_RELEASE_TAG := $(NPM_DRY_RUN_RELEASE_TAG)
npm-publish-dry-run: NPM_PUBLISH_FLAGS += --dry-run
npm-publish-dry-run: npm-publish

## npm-clean: remove generated npm packages
npm-clean:
	rm -rf $(NPM_PACKAGES_DIR)

## help: show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^##' Makefile | sed 's/## //g' | awk -F ':' '{printf "  %-12s %s\n", $$1, $$2}'
