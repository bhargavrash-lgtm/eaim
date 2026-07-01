# EAMI — Agent File Ownership & Module Boundaries
**Version:** 0.1.0  
**Owner:** Architect-EAMI agent  
**Rule:** No agent modifies files outside their boundary without an explicit handoff task from PM-EAMI. Violations block PR merge.

---

## The Golden Rule

If you are unsure whether a file is in your boundary, **stop and ask PM-EAMI**. Do not assume. Do not "just fix it while you're there." Cross-boundary changes require a written handoff task.

---

## Boundary Map

### Agent 1 — PM-EAMI
**Owns:**
```
ARCHITECTURE.md
BOUNDARIES.md
DECISIONS.md
ROADMAP.md
docs/
  specs/           ← feature specs, user stories
  decisions/       ← ADR records
tasks/             ← task breakdown files (markdown)
```
**Never touches:** any source code file. If a code change is needed based on a product decision, PM creates a task for the relevant dev agent.

---

### Agent 2 — Architect-EAMI
**Owns:**
```
api/
  openapi.yaml     ← THE contract. All agents read this. Only Architect writes it.
schema/
  schema.sql       ← canonical DB schema
  migrations/      ← numbered migration files
docs/
  architecture/    ← diagrams, data flow docs
```
**Also reviews (but does not own):**
- Any PR that changes an API endpoint signature
- Any PR that changes a database table structure
- Any PR that adds a new inter-service communication pattern

**Never touches:** service implementation code, UI code.

---

### Agent 3 — BE-Gateway
**Owns:**
```
eami-gateway/
  cmd/
    gateway/       ← main entrypoint
  internal/
    mcp/           ← MCP protocol handler
    identity/      ← agent registration, AI token issuance/validation
    node/          ← Serf gossip mesh, leader election
    proxy/         ← downstream tool proxy
    approval/      ← approval router, webhook dispatch
    audit/         ← audit log writer
    episode/       ← episode recorder (writes to vector DB)
    risk/          ← blast radius estimator
    config/        ← gateway YAML config loader
  eami-gateway.yaml   ← example config
  go.mod
  go.sum
  Dockerfile
```
**Reads (never writes):**
- `eami-policy/` — imports the policy library
- `api/openapi.yaml` — for understanding data contracts
- `schema/schema.sql` — for understanding DB schema

**Boundary notes:**
- The policy engine is NOT owned by BE-Gateway. It imports `eami-policy` as a library.
- Approval notification templates (Slack/Teams message format) are in `eami-gateway/internal/approval/templates/`. BE-Gateway owns those templates.

---

### Agent 4 — BE-Collector
**Owns:**
```
eami-collector/
  cmd/
    collector/     ← main entrypoint
  internal/
    ingest/        ← HTTP handler, schema validation, normalisation
    buffer/        ← SQLite write-ahead buffer
    forwarder/     ← batch sender to SaaS API
    config/        ← collector YAML config loader
  eami-collector.yaml  ← example config
  go.mod
  go.sum
  Dockerfile
  migrations/      ← SQLite schema for local buffer
```
**Also owns:**
```
eami-agent/        ← entire endpoint agent codebase (Windows)
  cmd/agent/
  internal/
    detection/
    payload/
    collector/     ← HTTP sender (client side of collector)
    config/
    service/       ← Windows service wiring
  eami-agent.yaml
  go.mod
  go.sum
```
**Reads (never writes):**
- `api/openapi.yaml` — ingest endpoint contract
- `schema/schema.sql` — understanding what fields the SaaS DB expects

---

### Agent 5 — BE-Policy
**Owns:**
```
eami-policy/       ← Go library package (not a binary)
  policy.go        ← exported Policy, Rule, Decision types
  evaluator.go     ← rule evaluation engine
  semantic.go      ← LLM-based semantic rule evaluation
  structural.go    ← JSON-match structural rule evaluation
  types.go         ← shared types
  testdata/        ← policy evaluation test fixtures
  policy_test.go
  semantic_test.go
  go.mod
  go.sum
```
**Also owns:**
```
eami-api/
  internal/
    policy/        ← policy CRUD handlers (reads/writes policy rules to DB)
```
**Reads (never writes):**
- `api/openapi.yaml` — policy API endpoints
- `schema/schema.sql` — policy_rules, policy_conditions tables

**Boundary notes:**
- BE-Policy writes the library. BE-Gateway imports it. If BE-Gateway needs a new capability in the policy engine, it creates a task for BE-Policy — it does not write policy evaluation code itself.
- The semantic evaluator makes LLM API calls. The LLM endpoint and API key config live in `eami-gateway/internal/config/` (owned by BE-Gateway), but the call logic is in `eami-policy/semantic.go` (owned by BE-Policy).

---

### Agent 6 — FE-Dashboard
**Owns:**
```
eami-ui/
  src/
    pages/
      dashboard/   ← Dashboard page
      discover/    ← Discover page
      finops/      ← FinOps page
      settings/    ← Settings page
    components/
      layout/      ← AppShell, Sidebar, Topbar, Navigation
      charts/      ← shared chart components (recharts wrappers)
      common/      ← Button, Badge, StatusPill, MetricCard, Table, etc.
    api/           ← generated API client (openapi-typescript output)
    stores/        ← Zustand stores (auth, org, ui)
    hooks/
      useEndpoints.ts
      useFinOps.ts
      useAlerts.ts
    lib/
      auth.ts      ← JWT handling, token refresh
      query.ts     ← TanStack Query client config
    main.tsx
    App.tsx
    router.tsx
  index.html
  vite.config.ts
  tailwind.config.ts
  tsconfig.json
  package.json
```
**Reads (never writes):**
- `api/openapi.yaml` — to regenerate the typed API client
- All other frontend page directories (reference only, for shared component usage)

**Boundary notes:**
- FE-Dashboard owns all shared components in `src/components/`. The other FE agents import these but do not modify them. If a shared component needs a change, FE-Dashboard owns that change (or receives a handoff task from PM).
- FE-Dashboard owns `App.tsx` and `router.tsx`. Route additions for other sections must be requested via a handoff task.

---

### Agent 7 — FE-Gateway
**Owns:**
```
eami-ui/
  src/
    pages/
      gateway/
        agents/    ← Agent registry page
        policies/  ← Policy rule builder page
        tools/     ← Tool connection manager page
        nodes/     ← Node cluster view page
    hooks/
      useAgents.ts
      usePolicies.ts
      useTools.ts
      useNodes.ts
```
**Reads (never writes):**
- `src/components/` — imports shared components, never modifies
- `src/api/` — imports generated client, never modifies
- `src/stores/` — reads auth/org state, never modifies
- `api/openapi.yaml` — reference for data shapes

---

### Agent 8 — FE-Ops
**Owns:**
```
eami-ui/
  src/
    pages/
      approvals/   ← Approval queue page
      memory/      ← Episode library page
      audit/       ← Audit log page
    hooks/
      useApprovals.ts
      useMemory.ts
      useAudit.ts
```
**Reads (never writes):**
- `src/components/` — imports shared components
- `src/api/` — imports generated client
- `src/stores/` — reads auth/org state
- `api/openapi.yaml` — reference

---

### Agent 9 — QA-EAMI
**Owns:**
```
eami-agent/
  *_test.go        ← Go test files (unit + integration)
eami-collector/
  *_test.go
eami-gateway/
  *_test.go
eami-policy/
  *_test.go
eami-api/
  *_test.go
eami-ui/
  src/**/*.test.ts
  src/**/*.test.tsx
  playwright/      ← E2E tests
  vitest.config.ts
.github/
  workflows/
    test.yml       ← CI test pipeline
```
**Also owns:**
```
testdata/          ← shared test fixtures, mock payloads
scripts/
  test-coverage.sh
```
**Boundary notes:**
- QA-EAMI writes tests in any service's directory, but only test files (`*_test.go`, `*.test.ts`, `playwright/`). Never modifies implementation files.
- QA-EAMI reviews every PR and must approve before merge.
- If QA finds a bug, it creates a task for the owning agent — it does not fix implementation code.

---

### Agent 10 — DevOps-EAMI
**Owns:**
```
docker-compose.yml
docker-compose.prod.yml
.env.example
.github/
  workflows/
    build.yml      ← CI build + publish
    deploy.yml     ← CD pipeline
scripts/
  install-agent.ps1   ← Windows agent install script
  install-agent.sh    ← Linux/Mac agent install
  seed-db.sh          ← DB seed for dev
  generate-api-client.sh  ← regenerate openapi-typescript client
eami-api/
  Dockerfile
eami-ui/
  Dockerfile
  nginx.conf
eami-gateway/
  Dockerfile      ← DevOps edits this; BE-Gateway owns the Go build inside
eami-collector/
  Dockerfile      ← same split
```
**Boundary notes:**
- DevOps owns `Dockerfile` and CI/CD yaml. The Go build commands inside Dockerfiles must be agreed with the relevant BE agent.
- DevOps owns `docker-compose.yml` environment variable wiring. Service-specific config files (`.yaml`) are owned by their respective BE agents.
- DevOps does not write Go or TypeScript code.

---

## Cross-Workspace File Delivery Protocol

When an agent delivers a file that lives in another agent's boundary (e.g. FE-Ops delivering a hook that FE-Dashboard must sync), the following rule applies:

**The delivering agent MUST output the full file contents verbatim in their task response.**

A confirmation like "the file exists in my workspace at path X" is not sufficient. File contents do not automatically transfer between agent workspaces. If the receiving agent cannot see the bytes, they cannot use the file.

**Correct pattern:**
> "Here is the full content of `src/hooks/useAlerts.ts` — paste this verbatim to replace your copy: [full file contents]"

**Incorrect pattern:**
> "The file is complete in my workspace. FE-Dashboard can sync from it."

This rule exists because we have hit cross-workspace sync failures on: `useApprovals.ts`, `useAudit.ts`, and `AlertsPage.tsx` — all causing multiple round-trips and wasted sessions.

---

## Shared Files — Modification Protocol

These files are read by everyone and must be modified carefully:

| File | Owner | Change protocol |
|---|---|---|
| `api/openapi.yaml` | Architect-EAMI | Any change requires Architect to update the file AND notify FE-Dashboard to regenerate the client |
| `schema/schema.sql` | Architect-EAMI | Any change requires a new migration file AND notification to all BE agents |
| `docker-compose.yml` | DevOps-EAMI | Changes require review from all BE agents whose service config changes |
| `ARCHITECTURE.md` | Architect-EAMI | PM and Architect jointly maintain |
| `DECISIONS.md` | PM-EAMI | PM writes; Architect reviews |

---

## Handoff Task Format

When you need another agent to do something, create a task file in `tasks/` with this format:

```markdown
# Task: [short title]
**From:** [your agent name]
**To:** [target agent name]
**Priority:** high | normal | low
**Blocked by:** [task ID if applicable]

## What I need
[clear description of what you need the other agent to do]

## Context
[why this is needed, what you've already done]

## Acceptance criteria
- [ ] [specific, testable outcome]
- [ ] [specific, testable outcome]

## Files involved
[list of files to create or modify]
```

---

## What "Never Touch" Means

"Never touch" means:
- Do not open the file in an edit context
- Do not copy-paste from it into your own file with modifications
- Do not instruct another agent to edit it on your behalf without a proper handoff

If you find a bug in another agent's file: document it, create a handoff task, and stop. Do not fix it yourself.
