# Makefile for agent-marketplace-packages
#
# Run `make help` for a list of targets.

# Image build/push settings.
# Same shape as agent-platform-backend's Makefile so dual-arch release
# workflow is uniform across the two repos: VERSION is the single source of
# truth (read from the VERSION file at the repo root), TAG is the
# fully-rendered image tag.
IMAGE       ?= agent-marketplace-api
REGISTRY    ?= quay.io/vmware-ai
PLATFORMS   ?= linux/amd64,linux/arm64
BUILDER     ?= agent-platform-builder
VERSION     := $(shell cat VERSION 2>/dev/null || echo dev)
# TAG is the version-stamped tag we push alongside :latest. Override
# from the CLI: `make release-images TAG=v0.1.0-20250729`.
TAG  ?= $(VERSION)-$(shell date -u +%Y%m%d)


BIN_DIR      ?= bin
GO          ?= go
LINUX_ARCH  ?= amd64
# ---- Build ----
.PHONY: build
build: ## Build both binaries (marketplace-api + agentpkg) into $(BIN_DIR)
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/marketplace-api ./cmd/marketplace-api
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/agentpkg        ./cmd/agentpkg

.PHONY: build-api
build-api: ## Build only marketplace-api
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/marketplace-api ./cmd/marketplace-api

.PHONY: build-agentpkg
build-agentpkg: ## Build only agentpkg CLI
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/agentpkg ./cmd/agentpkg

# Cross-compile agentpkg for Linux. Standalone — no dependency on `build`
# or `build-agentpkg`, since the host build isn't useful here and would
# just waste a build slot. Override the arch from the CLI:
#   make build-agentpkg-linux LINUX_ARCH=arm64
.PHONY: build-agentpkg-linux
build-agentpkg-linux: ## Cross-compile agentpkg for Linux (LINUX_ARCH=amd64|arm64, default amd64)
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=$(LINUX_ARCH) $(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/agentpkg-linux-$(LINUX_ARCH) ./cmd/agentpkg

.PHONY: build-linux
build-linux: ## Cross-compile marketplace-api for Linux amd64
	GOOS=linux GOARCH=amd64 $(MAKE) build-api

# ---- Test ----
.PHONY: test
test: ## Run unit tests
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint: ## Run go vet
	$(GO) vet ./...

# ---- OpenAPI ----
# The spec lives at docs/api/openapi.json (canonical, hand-edited).
# internal/server/openapi.json is a byte-identical copy consumed by
# `//go:embed` — regenerate with `make openapi-embed` after editing
# the canonical file.
.PHONY: openapi-embed
openapi-embed: ## Copy docs/api/openapi.json → internal/server/openapi.json for go:embed
	cp docs/api/openapi.json internal/server/openapi.json

.PHONY: openapi-check
openapi-check: ## Validate docs/api/openapi.json matches embed copy + apitypes types + router paths
	$(GO) run ./tools/openapi-check ./docs/api/openapi.json ./internal/server/openapi.json ./internal/apitypes

# ---- Container ----
.PHONY: docker-run
docker-run: docker-build ## Build + run locally (requires .env and config/config.yaml)
	@if [ ! -f deploy/.env ]; then \
		cp deploy/config/.env.example deploy/.env; \
		echo "Created deploy/.env with placeholder password — EDIT IT"; \
	fi
	@if [ ! -f deploy/compose/config.yaml ]; then \
		cp config/marketplace-api.example.yaml deploy/compose/config.yaml; \
		echo "Created deploy/compose/config.yaml from example"; \
	fi
	docker compose -f deploy/compose/docker-compose.yml --env-file deploy/.env up

.PHONY: docker-stop
docker-stop: ## Stop local stack
	docker compose -f deploy/compose/docker-compose.yml down

# Multi-arch build + push to $(REGISTRY). Tags the image with $(RELEASE_TAG)
# (versioned) + :latest at every supported platform in $(PLATFORMS). Always
# pushes — pushes are not undoable; keep prod tags deliberate.
#
# Notes:
#   - Requires the docker buildx plugin (Docker 19.03+). On Apple Silicon
#     hosts you may need to `docker buildx create --use` once.
#   - The default Dockerfile (deploy/docker/Dockerfile) is multi-arch capable
#     because its base images (golang:1.23-alpine, alpine:3.20) are
#     multi-arch manifests. Dockerfile.local uses a pre-built binary and
#     therefore needs one build per arch — switch DOCKERFILE below if you
#     choose that path.
#   - Override any of REGISTRY, IMAGE, TAG, PLATFORMS from the CLI.
.PHONY: release-images
release-images: ## Build + push multi-arch image to $(REGISTRY)
	docker buildx create --name $(BUILDER) --use --driver docker-container 2>/dev/null || true
	docker buildx build \
		--builder $(BUILDER) \
		--platform $(PLATFORMS) \
		--tag $(REGISTRY)/$(IMAGE):$(TAG) \
		--tag $(REGISTRY)/$(IMAGE):latest \
		--push \
		-f deploy/docker/Dockerfile \
		.
		

# ---- Cleanup ----
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help