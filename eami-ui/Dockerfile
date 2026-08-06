# eami-ui — Multi-stage Docker build
# Owner: FE-Dashboard (application code) / DevOps-EAMI (this file)
#
# Build context: ./eami-ui  (run from repo root via docker-compose)
#
# Targets (--target flag or docker-compose.yml target: key):
#   development   Vite dev server with hot reload; used by docker-compose locally
#   builder       Intermediate stage: npm ci + generate-client + vite build
#   production    nginx:alpine serving the compiled /app/dist; used in prod
#
# Expected source layout (FE-Dashboard to provide):
#   package.json
#   package-lock.json
#   vite.config.ts
#   tsconfig.json
#   src/                      <- React + TypeScript application source
#   public/                   <- static assets
#
# Required package.json scripts:
#   "dev"               Vite dev server  (CMD in development target)
#   "build"             Vite production build  (RUN in builder target)
#   "generate-client"   openapi-typescript codegen from api/openapi.yaml
#
# nginx.conf is co-located in this directory (eami-ui/nginx.conf).

# =============================================================================
# Target: development
# Used by: docker-compose.yml (local dev; hot reload via volume mount)
# =============================================================================
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS development

WORKDIR /app

# Install dependencies separately from source copy so this layer is cached
# unless package.json or package-lock.json changes.
COPY eami-ui/package*.json ./
RUN npm ci

# Copy source and the openapi spec (needed for generate-client at dev time)
COPY eami-ui/ .
COPY api/openapi.yaml /api/openapi.yaml

# 5173 — Vite dev server default port
EXPOSE 5173

# --host 0.0.0.0 makes Vite listen on all interfaces inside the container
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]

# =============================================================================
# Target: builder  (intermediate — not used directly by docker-compose)
# Produces /app/dist for the production target.
# =============================================================================
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS builder

WORKDIR /app

COPY eami-ui/package*.json ./
RUN npm ci

# Copy UI source and the openapi spec that generate-client needs.
# The script references ../api/openapi.yaml relative to WORKDIR /app,
# which resolves to /api/openapi.yaml — place it there directly.
COPY eami-ui/ .
COPY api/openapi.yaml /api/openapi.yaml

# generate-client runs openapi-typescript against ../api/openapi.yaml and writes
# typed client code to src/api/. Must run before vite build so types are available.
RUN npm run generate-client && npm run build

# =============================================================================
# Target: production
# Used by: docker-compose.prod.yml (nginx:alpine serves the compiled SPA)
# =============================================================================
FROM nginx:alpine@sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752 AS production

# Copy the Vite production build from the builder stage
COPY --from=builder /app/dist /usr/share/nginx/html

# SPA routing config: all paths serve index.html; static assets cached 1 year
COPY eami-ui/nginx.conf /etc/nginx/conf.d/default.conf

# 80 — nginx HTTP port
EXPOSE 80

# The official nginx:alpine image does NOT set a USER directive -- the master
# process starts and stays root by design, since binding port 80 requires
# root privilege. It never serves requests itself: the master's own
# nginx.conf (baked into the base image, not overwritten by default.conf
# above) sets `user nginx;`, so every worker process -- the ones that
# actually accept connections and serve files -- drops to the unprivileged
# `nginx` user immediately after fork. Root exposure is confined to the
# master's own startup/reload/signal-handling code, not request handling.
