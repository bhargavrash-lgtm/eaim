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

# Local dependency: no network fetch needed, must be present before go mod download
COPY eami-policy/ ./eami-policy/

# Module manifests only — this layer is cached independently of source changes,
# so go mod download only re-runs when go.mod or go.sum change.
COPY eami-gateway/go.mod eami-gateway/go.sum ./eami-gateway/

WORKDIR /build/eami-gateway
RUN go mod download

# Full gateway source (overwrites the manifests copied above; content is identical)
COPY eami-gateway/ ./

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
