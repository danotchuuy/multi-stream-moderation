# syntax=docker/dockerfile:1
#
# Multi-arch build for the Go API + built React UI, served as a single
# binary. Build with `make docker-build` / push with `make docker-push`
# (see the Makefile) rather than invoking `docker build` directly, since
# a real multi-platform result requires buildx with a docker-container
# builder — a plain `docker build` will silently only build for your
# host's own platform.

# ---- Build the web UI --------------------------------------------------
# The UI build's output is the same regardless of the final image's
# target platform, so this stage is pinned to the build machine's own
# platform (BUILDPLATFORM) rather than running once per --platform
# target under QEMU emulation for no benefit.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY web/ ./
RUN npm run build

# ---- Cross-compile the Go server ---------------------------------------
# Also pinned to BUILDPLATFORM: Go's own cross-compiler produces the
# target-arch binary directly (CGO_ENABLED=0, no cgo anywhere in this
# module), so running the Go toolchain itself under emulation per target
# platform would only make the build slower, not more correct.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /app/server ./cmd/server

# An empty, writable data directory for the encrypted OAuth session
# stores (see internal/server/auth.go). Created here, where a shell and
# root exist, and copied into the shell-less runtime image below with
# the right ownership for its non-root user — the runtime image can't
# mkdir this itself.
RUN mkdir -p /data

# ---- Runtime image ------------------------------------------------------
# distroless "static" (not "scratch"): this app makes outbound HTTPS
# calls to Twitch/Kick/YouTube/Google, so it needs the CA certificate
# bundle distroless ships; "nonroot" runs the whole container as an
# unprivileged user by default.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.source="https://github.com/danotchuuy/multi-stream-moderation" \
      org.opencontainers.image.description="Locally run application for moderating chat from multiple streams in a single place."

COPY --from=go-builder --chown=nonroot:nonroot /app /app
COPY --from=web-builder --chown=nonroot:nonroot /src/web/dist /app/web/dist
COPY --from=go-builder --chown=nonroot:nonroot /data /data

WORKDIR /app
USER nonroot:nonroot

# Mount a volume at /data to keep users signed in across container
# restarts/upgrades; without one, the sessions still work, they just
# won't survive the container being recreated.
ENV PORT=8080 \
    UI_DIST_DIR=/app/web/dist \
    SESSION_STORE_FILE=/data/sessions.enc \
    KICK_SESSION_STORE_FILE=/data/kick-sessions.enc \
    YOUTUBE_SESSION_STORE_FILE=/data/youtube-sessions.enc

EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/server"]
