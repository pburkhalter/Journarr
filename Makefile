VERSION ?= dev
DIST := backend/internal/web/dist

.PHONY: dev backend-dev frontend-dev frontend build docker check clean

## dev: run backend (:8484) — start `make frontend-dev` in a second terminal
backend-dev:
	cd backend && JOURNARR_LISTEN=:8484 DB_PATH=./dev.db LOG_FORMAT=text go run ./cmd/journarr

frontend-dev:
	cd frontend && npm run dev

## frontend: production build into the Go embed target
frontend:
	cd frontend && npm run build
	printf '*\n!.gitignore\n' > $(DIST)/.gitignore

## build: full production binary (frontend embedded)
build: frontend
	cd backend && CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.versionStr=$(VERSION)" \
		-o ../bin/journarr ./cmd/journarr

docker:
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t ghcr.io/pburkhalter/journarr:$(VERSION) .

check:
	cd backend && go vet ./... && go test ./...
	cd frontend && npm run check

clean:
	rm -rf bin frontend/.svelte-kit
	find $(DIST) -mindepth 1 -not -name '.gitignore' -delete
