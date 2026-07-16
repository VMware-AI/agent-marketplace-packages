# Makefile for agent-marketplace-packages
#
# Run `make help` for a list of targets.

BIN_DIR      ?= bin
IMAGE        ?= agent-marketplace/api
IMAGE_TAG    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
GO          ?= go

# ---- Build ----
.PHONY: build
build: ## Build marketplace-api binary
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/marketplace-api ./cmd/marketplace-api

.PHONY: build-linux
build-linux: ## Cross-compile marketplace-api for Linux amd64
	GOOS=linux GOARCH=amd64 $(MAKE) build

# ---- Test ----
.PHONY: test
test: ## Run unit tests
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint: ## Run go vet
	$(GO) vet ./...

# ---- Container ----
.PHONY: docker-build
docker-build: ## Build container image
	docker build -f deploy/docker/Dockerfile -t $(IMAGE):$(IMAGE_TAG) -t $(IMAGE):latest .

.PHONY: docker-run
docker-run: docker-build ## Build + run locally (requires .env and config/config.yaml)
	@if [ ! -f deploy/.env ]; then \
		cp deploy/.env.example deploy/.env; \
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

.PHONY: docker-push
docker-push: docker-build ## Push image to registry (override IMAGE for your registry)
	docker push $(IMAGE):$(IMAGE_TAG)
	docker push $(IMAGE):latest

# ---- Cleanup ----
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help