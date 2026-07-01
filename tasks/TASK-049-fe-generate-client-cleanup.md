# Task: Run generate-client and remove @ts-expect-error directives
**From:** PM-EAMI  
**To:** FE-Dashboard  
**Priority:** normal  
**Blocked by:** TASK-036, TASK-037, TASK-041, TASK-048 (all openapi.yaml additions must be done first)

## What I need

The UI codebase has `@ts-expect-error` directives left over from before the openapi schema was complete. Now that the API contract is stable, regenerate the client and remove them.

### Step 1 — Regenerate the API client

```bash
cd eami-ui
npm run generate-client
```

This runs `openapi-typescript` against `../api/openapi.yaml` and regenerates `src/api/schema.ts`.

### Step 2 — Fix type errors

After regenerating, run:
```bash
npx tsc --noEmit
```

For each `@ts-expect-error` directive, either:
1. **Remove it** if the type error no longer exists (schema now covers the type).
2. **Replace it** with a proper type cast if the error is legitimate (e.g. a union type mismatch at a callsite).
3. **Fix the underlying call** if the hook is using the wrong shape.

Do NOT suppress errors with `// @ts-ignore` — that bypasses the type check entirely.

### Step 3 — Verify all pages render

With the stack running:
1. Navigate to each page: Dashboard, Discover, FinOps, Gateway (Agents, Policies, Tools, Nodes), Ops (Approvals, Audit, Alerts, Memory), Settings.
2. Check browser console for runtime errors.
3. Fix any broken data binding caused by schema changes (field renames, etc.).

### Step 4 — Add missing hook types

After generate-client, some response types that were manually typed may now be fully typed by the generated schema. Replace manual `interface Foo { ... }` declarations in hook files with the generated types from `schema.ts` where available.

## Acceptance criteria

- [ ] `npm run generate-client` completes without error
- [ ] `tsc --noEmit` exits 0 with zero errors
- [ ] Zero `@ts-expect-error` directives remain in the codebase
- [ ] Zero `@ts-ignore` directives
- [ ] All 11 pages load without runtime console errors
- [ ] `npm run build` succeeds (Vite production build)

## Files to modify

- `eami-ui/src/api/schema.ts` — regenerated (do not edit manually)
- `eami-ui/src/hooks/*.ts` — remove @ts-expect-error, fix types
- `eami-ui/src/pages/**/*.tsx` — fix any data binding issues

## Files to read first

- `eami-ui/src/hooks/useAlerts.ts` — known to have @ts-expect-error directives
- `api/openapi.yaml` — the source of truth for schema types
