# Task: Fix eami-ui Docker build in CI
**From:** PM-EAMI
**To:** FE-Dashboard
**Priority:** high
**Blocked by:** none

## What I need

The `Docker — eami-ui` CI job is failing. Fix it so the eami-ui image builds and pushes to GHCR successfully.

**Failed run:** https://github.com/bhargavrash-lgtm/eaim/actions/runs/28502936908
**Image target:** `ghcr.io/bhargavrash-lgtm/eami-ui:latest`

## Context

In a previous CI fix round, the build context for eami-ui was changed from `./eami-ui` to `.` (repo root) so that `api/openapi.yaml` would be accessible. The Dockerfile was updated accordingly — all `COPY` paths were prefixed with `eami-ui/` and the openapi spec was placed at `/api/openapi.yaml` inside the container.

The five installer artifact jobs all passed in that same run, so the failure is isolated to the Docker build step for eami-ui.

Relevant files:
- `eami-ui/Dockerfile` — build context is now repo root
- `.github/workflows/build.yml` — `docker-context: .` for eami-ui job
- `api/openapi.yaml` — must be accessible during build

## Acceptance criteria

- [ ] `Docker — eami-ui` job passes in CI
- [ ] eami-ui image is pushed to `ghcr.io/bhargavrash-lgtm/eami-ui`
- [ ] `npm run generate-client && npm run build` succeeds inside the container (no TypeScript errors)
- [ ] nginx serves `index.html` on port 80 (smoke-tested via `docker run -p 8080:80 ... curl localhost:8080`)

## Files to modify

- `eami-ui/Dockerfile` (if needed)
- `.github/workflows/build.yml` (if needed)
- Any TypeScript source files if there are compiler errors blocking the build
