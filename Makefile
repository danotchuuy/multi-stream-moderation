.PHONY: dev dev-server dev-web build build-web build-server run install test

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
