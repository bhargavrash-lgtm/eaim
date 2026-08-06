# eami-gateway Dockerfile — multi-stage, static binary, no CGO.
# Build context: repo root (required — go.work and eami-policy live there).
#
# docker-compose:  context: .   dockerfile: eami-gateway/Dockerfile
# CI (build.yml):  context: .   file: eami-gateway/Dockerfile

# ── Stage 1: build ─────────────────────────────────────────────────────────
# golang:1.25-alpine -- pinned by digest (pgx v5.9.2/x/text v0.39.0 require Go >=1.25.0)
FROM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS builder

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
#
# identity/tokens.go self-generates and persists an RSA signing key on first
# boot, to whichever of two paths applies:
#   - GATEWAY_JWT_KEY_PATH set (docker-compose's real deployment):
#     /certs/gateway.key, the gateway_certs named volume.
#   - GATEWAY_JWT_KEY_PATH unset (config.go's validate() default, any other
#     deployment method -- bare `docker run`, a k8s manifest, a future
#     compose profile): /var/lib/eami-gateway/gateway.key, the VOLUME
#     declared below.
# A brand-new named/anonymous volume with nothing at that path in the image
# defaults to root:root ownership, which the non-root USER below can't
# write to -- baking already-owned directories into the image at both
# possible paths means either volume inherits correct ownership no matter
# which one actually ends up used.
RUN mkdir -p /out && mkdir -p /out/certs && mkdir -p /out/varlib && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/eami-gateway ./cmd/gateway

# ── Stage 2: runtime ───────────────────────────────────────────────────────
# scratch has no registry manifest/tag to pin -- there is nothing to float.
FROM scratch
COPY --from=builder --chown=65532:65532 /out/eami-gateway /eami-gateway
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chown=65532:65532 /out/certs /certs
COPY --from=builder --chown=65532:65532 /out/varlib /var/lib/eami-gateway
# Nonroot, numeric -- scratch has no /etc/passwd, so a username can't resolve;
# a bare UID:GID needs no lookup and still sets the process's real identity.
USER 65532:65532
# /etc/eami-gateway (the --config default) is read-only -- a missing file
# there is already handled gracefully (see config.go's rules-path fallback),
# so it doesn't need the same write-ownership treatment as the two paths
# above.
VOLUME ["/etc/eami-gateway", "/var/lib/eami-gateway"]
EXPOSE 8080 3000
ENTRYPOINT ["/eami-gateway"]
CMD ["--config", "/etc/eami-gateway/eami-gateway.yaml"]
