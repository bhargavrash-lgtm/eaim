# NOTES.md — out-of-scope suggestions logged during task work

Per CLAUDE.md's hard rules: no refactoring outside an assigned task's
scope; suggestions belong here instead of being silently made.

## 2026-07-24 — `toolcreds.go`'s `Decrypt` doc comment is now stale

**File:** `eami-api/internal/toolcreds/toolcreds.go`, `Decrypt`'s doc comment
("Not called from any production HTTP path -- credentials are write-only
from the API's perspective...").

**Why it's stale:** B-023 (`eami-api/internal/api/tool_connectivity.go`)
is now `Decrypt`'s first production caller — `TestTool` decrypts stored
credentials to run a real connectivity check. The comment was accurate
when written (B-022) but not anymore.

**Why not fixed now:** `toolcreds.go` is B-022's frozen file and wasn't in
B-023's `MAY MODIFY` scope (`eami-api/internal/api/tools.go` + a new
connectivity helper file only).

**Suggested fix:** update the comment to describe both callers — B-022's
retrieval-proof tests (`toolcreds_test.go`) and B-023's `TestTool` — or
just soften it to "credentials are write-only from the general API's
perspective; the one exception is TestTool's own connectivity check,
which decrypts to attempt a real connection and never returns or logs
the result."
