# Stage 1: frontend
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build
# npm run build writes to ../backend/internal/web/dist (svelte.config.js),
# which inside this stage is /src/backend/internal/web/dist

# Stage 2: backend
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
COPY --from=frontend /src/backend/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.versionStr=${VERSION}" \
    -o /out/journarr ./cmd/journarr

# Stage 3: runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/journarr /journarr
EXPOSE 8484
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/journarr", "healthcheck"]
ENTRYPOINT ["/journarr"]
