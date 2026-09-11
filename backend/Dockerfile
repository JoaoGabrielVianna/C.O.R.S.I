# syntax=docker/dockerfile:1.7

# ─── build stage ────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache module downloads in a dedicated layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/corsi   ./cmd/corsi && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# ─── runtime stage ──────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S corsi && adduser -S corsi -G corsi

WORKDIR /app
COPY --from=build /out/corsi   /app/corsi
COPY --from=build /out/migrate /app/migrate
COPY migrations                /app/migrations
COPY deploy/entrypoint.sh      /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh && chown -R corsi:corsi /app

USER corsi:corsi
EXPOSE 8080

# Hosted platforms (EasyPanel, Fly, Railway) inject PORT. The entrypoint
# rewrites PORT → HTTP_ADDR and optionally runs migrations before exec'ing
# the server binary.
ENV PORT=8080 \
    RUN_MIGRATIONS=true \
    LOG_LEVEL=info \
    LOG_FORMAT=json \
    WORKSPACE_REQUIRE_HEADER=true

ENTRYPOINT ["/app/entrypoint.sh"]
