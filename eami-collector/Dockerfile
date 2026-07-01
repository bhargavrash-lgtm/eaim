# eami-collector — Multi-stage Docker build
# Owner: BE-Collector (application code) / DevOps-EAMI (this file)
#
# Build context: ./eami-collector  (run from repo root via docker-compose)
#
# Stages:
#   builder     golang:1.24-alpine — compiles the static Go binary
#   runtime     alpine:3.19        — minimal production image (~10 MB)
#
# Expected source layout (BE-Collector to provide):
#   go.mod
#   go.sum
#   cmd/collector/main.go    <- binary entry point
#   internal/...

# =============================================================================
# Stage 1: builder
# =============================================================================
FROM golang:1.24-alpine AS builder

# Install build tools needed by some Go packages (git for VCS stamp, gcc not
# needed because CGO_ENABLED=0, but ca-certificates is needed for go mod download
# to reach the module proxy over HTTPS).
RUN apk add --no-cache ca-certificates git

WORKDIR /app

# Copy dependency manifests first — Docker layer caches this independently of
# source changes, so `go mod download` only re-runs when go.mod/go.sum change.
COPY go.mod ./
RUN go mod download

# Copy the full source tree and compile.
# CGO_ENABLED=0  — pure-Go binary, no libc dependency in the runtime image
# GOOS/GOARCH    — explicit for clarity (matches the runner: Linux AMD64)
# -ldflags -w -s — strip DWARF debug info and symbol table; reduces binary ~30%
# -mod=mod       — allow go to update go.sum on the fly (go.sum not committed)
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -mod=mod -ldflags="-w -s" -o /collector ./cmd/collector/

# =============================================================================
# Stage 2: runtime
# =============================================================================
FROM alpine:3.19

# ca-certificates: needed for outbound TLS to eami-api (HTTPS)
# tzdata:          needed if the service formats timestamps with a local timezone
RUN apk add --no-cache ca-certificates tzdata su-exec

# Run as a dedicated non-root service account
RUN addgroup -S eami && adduser -S eami -G eami

WORKDIR /app

# Copy only the compiled binary and entrypoint from the builder stage
COPY --from=builder /collector /app/collector
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# /app/data is the default mount point for the SQLite buffer DB.
# The entrypoint script chowns it at runtime to handle Docker volume ownership.
RUN mkdir -p /app/data

# Collector ingest port — endpoint agents POST reports here
EXPOSE 8888

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/collector"]
