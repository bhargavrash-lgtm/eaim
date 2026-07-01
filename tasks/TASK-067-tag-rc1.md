# Task: Tag v1.0.0-rc1 to produce CI installer artifacts
**From:** PM-EAMI
**To:** DevOps-EAMI
**Priority:** high
**Blocked by:** TASK-066 (bcrypt fix, done)

## What I need

Push a `v1.0.0-rc1` git tag so the CI build.yml workflow runs and produces the five
installer artifacts needed for TASK-054 smoke tests:

- `eami-agent-1.0.0-rc1-windows-amd64.msi`
- `eami-agent-1.0.0-rc1-darwin-amd64.pkg`
- `eami-agent-1.0.0-rc1-darwin-arm64.pkg`
- `eami-agent_1.0.0-rc1_amd64.deb`
- `eami-agent_1.0.0-rc1_amd64.rpm`

## Context

TASK-054 (installer smoke tests) cannot run without real artifacts, and artifacts only
build on a `v*` tag. The solution is a release candidate tag: smoke test the rc1 artifacts
(TASK-054), fix anything that fails, then tag v1.0.0. This breaks the circular dependency
without skipping the smoke test gate.

This is NOT the final release — do not write CHANGELOG.md or create the GitHub Release yet.
That is TASK-055.

## Acceptance criteria

- [ ] `git tag -a v1.0.0-rc1 -m "release candidate 1"` pushed to origin
- [ ] CI build.yml workflow completes successfully (green)
- [ ] All five installer artifacts are attached to the rc1 pre-release on GitHub
- [ ] Link to the GitHub Actions run pasted into this task or TASK-054-results.md

## Files to modify

None — tag only.
