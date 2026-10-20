GO        ?= go
IMAGE     ?= distortioner
GOLANGCI  ?= v2.15.0
VERSION   ?=
GIT_VER   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(GIT_VER)

.DEFAULT_GOAL := help
.PHONY: help build run test cover vet fmt fmt-check lint vuln check docker itest itest-bench release release-dry

help: ## Show this help
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/distortioner ./cmd/distortioner

run: ## Run locally with variables from .env
	set -a && . ./.env && set +a && DISTORTIONER_LOG_FORMAT=text $(GO) run ./cmd/distortioner

test: ## Unit tests with the race detector
	$(GO) test -race -count=1 ./...

cover: ## Unit tests with a coverage report
	$(GO) test -race -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

vet: ## go vet, including integration tests
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if the code is not formatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

lint: ## golangci-lint (local binary if present, Docker otherwise)
	@if command -v golangci-lint >/dev/null; then golangci-lint run; \
	else docker run --rm -v "$(CURDIR)":/src -w /src golangci/golangci-lint:$(GOLANGCI) golangci-lint run; fi

vuln: ## Check dependencies for known vulnerabilities
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

check: fmt-check vet lint test ## Everything CI runs, except Docker

docker: ## Build the runtime image
	docker build --build-arg VERSION=$(GIT_VER) -t $(IMAGE) .

itest: ## Integration tests with real ffmpeg and ImageMagick, in Docker
	docker build --target itest -t $(IMAGE)-itest .
	docker run --rm $(IMAGE)-itest go test -tags integration -count=1 ./...

itest-bench: ## Benchmark the video pipeline in Docker
	docker build --target itest -t $(IMAGE)-itest .
	docker run --rm $(IMAGE)-itest go test -tags integration -run '^$$' -bench DistortVideo -benchtime 3x ./internal/media/

release: ## Tag and push a release: make release VERSION=1.2.3
	@scripts/release.sh "$(VERSION)"

release-dry: ## Run every release check without tagging
	@DRY_RUN=1 scripts/release.sh "$(VERSION)"
