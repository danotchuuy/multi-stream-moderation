.PHONY: dev dev-server dev-web build build-web build-server run install test \
	docker-builder docker-build docker-build-local docker-push

# Run the Go API and the Vite dev server together (Ctrl+C stops both).
dev:
	@trap 'kill 0' EXIT; \
	$(MAKE) dev-server & \
	$(MAKE) dev-web & \
	wait

dev-server:
	go run ./cmd/server

dev-web:
	cd web && npm run dev

install:
	cd web && npm install

# Build the UI into web/dist, then the Go binary that can serve it.
build: build-web build-server

build-web:
	cd web && npm run build

build-server:
	go build -o bin/server ./cmd/server

run: build
	./bin/server

test:
	go test ./...

# --- Docker -------------------------------------------------------------
#
# Multi-arch images are built with `docker buildx` (see Dockerfile,
# which relies on buildx-only features — plain `docker build` can't
# produce them). Override DOCKER_IMAGE with your own Docker Hub repo,
# e.g.:
#   make docker-push DOCKER_IMAGE=youruser/multi-stream-moderation DOCKER_TAG=v1.0.0
# `docker login` first — pushing isn't done on your behalf.
#
# Every build/push applies two tags in one invocation (buildx builds
# once, tags twice — it doesn't rebuild per tag): the moving DOCKER_TAG
# (default "latest") and an immutable, date-coded DOCKER_DATE_TAG, so you
# can always pin to a specific day's build even after "latest" moves on.
# Override DOCKER_DATE_TAG directly if you need a second same-day build
# to get its own tag, e.g. DOCKER_DATE_TAG=$(DOCKER_DATE_TAG)-2.

DOCKER_IMAGE     ?= changeme/multi-stream-moderation
DOCKER_TAG       ?= latest
DOCKER_DATE_TAG  ?= $(shell date -u +%Y.%m.%d)
DOCKER_PLATFORMS ?= linux/amd64,linux/arm64
DOCKER_BUILDER   ?= multi-stream-moderation-builder

# Ensures a buildx builder that can actually produce multi-platform
# images exists and is selected. The default "docker" driver only
# supports the host's own platform; multi-platform output needs a
# "docker-container" builder.
docker-builder:
	@docker buildx inspect $(DOCKER_BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(DOCKER_BUILDER) --driver docker-container --use
	docker buildx use $(DOCKER_BUILDER)

# Validates that the image builds for every platform in
# DOCKER_PLATFORMS. A multi-platform result can't be loaded into the
# local Docker image store (only pushed, or built one platform at a time
# with --load) — this proves the build succeeds, it doesn't leave an
# image anywhere to run. Use docker-build-local to actually run one.
docker-build: docker-builder
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--tag $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--tag $(DOCKER_IMAGE):$(DOCKER_DATE_TAG) \
		.

# Builds for your machine's own platform only and loads the result into
# Docker, so you can `docker run` it locally to test.
docker-build-local: docker-builder
	docker buildx build --load \
		--tag $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--tag $(DOCKER_IMAGE):$(DOCKER_DATE_TAG) \
		.

# Builds for every platform in DOCKER_PLATFORMS and pushes the result to
# Docker Hub as a single multi-arch manifest, under both tags. Requires
# `docker login`.
docker-push: docker-builder
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--tag $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--tag $(DOCKER_IMAGE):$(DOCKER_DATE_TAG) \
		--push \
		.
