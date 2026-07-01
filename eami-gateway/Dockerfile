# eami-gateway Dockerfile — multi-stage, static binary, no CGO.
# Build context: repo root (required — go.work and eami-policy live there).
#
# docker-compose:  context: .   dockerfile: eami-gateway/Dockerfile
# CI (build.yml):  context: .   file: eami-gateway/Dockerfile

# ── Stage 1: build ─────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

# Never auto-download a newer Go toolchain, even if a transitive dep
# requests one via a 'toolchain' directive in its go.mod.
ENV GOTOOLCHAIN=local

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /build

# Go workspace file — tells the toolchain where to find local modules,
# including eami-policy which is required by gateway via a replace directive.
# Matches go.work and go.work.sum (copied if present).
COPY go.work* ./

# All local modules referenced by go.work — must be present before go mod download.
# Only go.mod/go.sum copied first so this layer is cached independently of source.
COPY eami-policy/ ./eami-policy/
COPY eami-gateway/go.mod eami-gateway/go.sum ./eami-gateway/
COPY eami-api/go.mod eami-api/go.sum ./eami-api/
COPY eami-collector/go.mod eami-collector/go.sum ./eami-collector/
COPY eami-agent/go.mod eami-agent/go.sum ./eami-agent/

WORKDIR /build/eami-gateway
RUN go mod download

# Full gateway source (overwrites the manifests copied above; content is identical)
COPY eami-gateway/ ./
COPY eami-policy/ ./eami-policy/

# Build static binary — CGO_ENABLED=0 ensures no libc dependency in the runtime image
RUN mkdir -p /out && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/eami-gateway ./cmd/gateway

# ── Stage 2: runtime ───────────────────────────────────────────────────────
FROM scratch
COPY --from=builder /out/eami-gateway /eami-gateway
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
VOLUME ["/etc/eami-gateway", "/var/lib/eami-gateway"]
EXPOSE 8080 3000
ENTRYPOINT ["/eami-gateway"]
CMD ["--config", "/etc/eami-gateway/eami-gateway.yaml"]
