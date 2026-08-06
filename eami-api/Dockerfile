# ── Stage 1: build ─────────────────────────────────────────────────────────────
# golang:1.25-alpine -- pinned by digest (pgx v5.9.2/x/text v0.39.0 require Go >=1.25.0)
FROM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS builder

WORKDIR /app

# Download dependencies first (cache layer).
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully-static binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /eami-api ./cmd/api

# auth.Service self-generates and persists an RSA signing key to
# API_JWT_KEY_PATH (/certs/api.key, docker-compose's api_certs named
# volume) on first boot. A brand-new named volume with nothing at that
# path in the image defaults to root:root ownership, which the non-root
# USER below can't write to -- baking an already-owned /certs directory
# into the image here means the volume inherits that ownership instead.
RUN mkdir -p /out/certs

# ── Stage 2: runtime ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12@sha256:a9fcaedd4c9b59e12dd65d954f0b5044f19b0647a8a3712e77205df9e7b102cd

WORKDIR /app

COPY --from=builder --chown=65532:65532 /eami-api /eami-api
COPY --chown=65532:65532 eami-api.yaml /app/eami-api.yaml
COPY --from=builder --chown=65532:65532 /out/certs /certs

# Distroless's nonroot UID/GID -- present in the image regardless of tag.
# Numeric so it resolves with no /etc/passwd lookup needed.
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/eami-api"]
CMD ["--config", "/app/eami-api.yaml"]
