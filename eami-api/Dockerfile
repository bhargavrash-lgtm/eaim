# ── Stage 1: build ─────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Download dependencies first (cache layer).
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully-static binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /eami-api ./cmd/api

# ── Stage 2: runtime ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /eami-api /eami-api
COPY eami-api.yaml /app/eami-api.yaml

EXPOSE 8080

ENTRYPOINT ["/eami-api"]
CMD ["--config", "/app/eami-api.yaml"]
