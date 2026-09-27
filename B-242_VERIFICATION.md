# B-242 Verification Record — invite of an existing email: one fixed 409 (option 2)

Written 2026-09-27 by Claude Code, at founder direction: "Option 2. Build it now — generic 409 for any existing email, same-org or other-org, no raw database error text … Option 1 remains the real fix, correctly deferred until email sending exists." Raw logs are in session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `b242_live.log`, `b242_mutation.log`, `b242_api_full.log`, `b242_before.txt` and `b242_after.txt`.

## 1. Change
`InviteUser` (`eami-api/internal/api/users.go`):
- **Existing email:** any unique violation from `CreateInvitedUser` now returns one fixed `409 {"code":"conflict","message":"this email cannot be invited"}`. That covers an email in the same org, another org, or on a soft-deleted account.
- **Other failures:** a generic `500 "could not create invite"`, logged with `slog`. There is no `err.Error()` echo on either path.
- **Contract:** `api/openapi.yaml` already documents a 409 for this route, so there is no contract change.

## 2. What this does and does not fix — corrected, please read

**Correction to the premise, found by the code review and confirmed by the live pre-fix run below.** Same-org and cross-org duplicates were **already** byte-identical before this change: both failed on the same global `users_email_key` constraint, and the echoed text never contained the email. So this change does **not** make cross-org existence "no longer distinguishable from same-org". It was never distinguishable in-band.

**What the change actually does:**
- removes the DB internals (constraint name, SQLSTATE) from the response;
- replaces an unexplained 500 with a designed 409;
- keeps every conflict case on one fixed response.

**The existence oracle is not narrowed.** The accurate residual, from the security review:
- An org admin sees a 409 for an email that is **not** in their own `GET /v1/users` list. That shows the exact-case email is registered in another tenant, unless it is one of their own deactivated users.
- This works in bulk: the endpoint has no rate limit.
- Combined with Login's pre-existing `"account uses SSO"` message and bcrypt timing (see §6), the admin can also tell whether that account is pending, active or deleted.

Closing this fully needs out-of-band invite delivery (option 1), plus separate fixes for the Login oracles and email normalization.

## 3. Automated verification
```
eami-api: go build ./... ; go vet ./... → clean (users.go was already gofmt-flagged at HEAD for CRLF, and the flag is unchanged)
eami-api: go test -count=1 -v ./... → every package ok; top-level PASS=496 FAIL=0 SKIP=0 (495 + 1 new)
```
**New test:** `invite_enumeration_pg_test.go` (real Postgres).
- An email in the same org, in another org, and on a soft-deleted account in another org all return a byte-identical 409.
- None of `users_email_key`, `duplicate`, `SQLSTATE`, `23505` or the email itself appears in the body.
- No `users` or `invite_tokens` rows are written.
- A new email still gets 201 with an invite link.

**Mutation checks** (`b242_mutation.log`, verbatim):
```
=== M1: pre-fix behaviour (unique violation falls to a 500 echoing err.Error())
--- FAIL: TestInviteUser_ExistingEmailUniform409_RealDB (0.24s)
    invite_enumeration_pg_test.go:60: email already in this org = 500 want 409: {"code":"internal_error","message":"ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
FAIL	github.com/eami/api/internal/api	0.519s

=== M2: 409 kept but the DB error text echoed as the message
--- FAIL: TestInviteUser_ExistingEmailUniform409_RealDB (0.24s)
    invite_enumeration_pg_test.go:62: email already in this org: body "{\"code\":\"conflict\",\"message\":\"ERROR: duplicate key value violates unique constraint \\\"users_email_key\\\" (SQLSTATE 23505)\"}\n", want the one fixed "{\"code\":
FAIL	github.com/eami/api/internal/api	0.519s

=== M3: same-org duplicates distinguished from other-org ones (a pre-check with its own message)
--- FAIL: TestInviteUser_ExistingEmailUniform409_RealDB (0.20s)
    invite_enumeration_pg_test.go:62: email already in this org: body "{\"code\":\"conflict\",\"message\":\"user already exists in this org\"}\n", want the one fixed "{\"code\":\"conflict\",\"message\":\"this email cannot be invited\"}\n" (
FAIL	github.com/eami/api/internal/api	0.476s

=== control: source restored
ok  	github.com/eami/api/internal/api	0.548s
```

## 4. Live verification (`b242_live.log`, verbatim; invite tokens redacted)
Org A's admin (a throwaway org) invited:
- an email already in org A;
- an email registered in throwaway org B;
- a new email.

This ran once on the pre-fix container (created 2026-09-27T17:17:37Z) and once on the fixed build (created 2026-09-27T17:36:37Z).
```
── PRE-FIX build
invite email already in MY org (b242-same@)          : 500 {"code":"internal_error","message":"ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
invite email registered in ANOTHER org (b242-other@) : 500 {"code":"internal_error","message":"ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
invite a NEW email (b242-new-pre@)                   : 201 {"user":{"id":"e9007cad-3522-4e24-b33d-facce7748665","email":"b242-new-pre@example.test","role":"viewer","org_id":"92f3d444-78e2-47a2-b7ca-a071dec895b8","created_at":
b242 user rows before / after (only the new invite adds one) : 3 / 4
── FIXED build
invite email already in MY org (b242-same@)          : 409 {"code":"conflict","message":"this email cannot be invited"}
invite email registered in ANOTHER org (b242-other@) : 409 {"code":"conflict","message":"this email cannot be invited"}
invite a NEW email (b242-new-post@)                  : 201 {"user":{"id":"7c541a4f-1625-48ae-ac39-5e177505ae3c","email":"b242-new-post@example.test","role":"viewer","org_id":"92f3d444-78e2-47a2-b7ca-a071dec895b8","created_at"
b242 user rows before / after (only the new invite adds one) : 4 / 5
```
**Cleanup:** both throwaway orgs were deleted (cascading their users and invite tokens), along with the refresh tokens and the fixture password file. `diff b242_before.txt b242_after.txt` is **identical**, including the audit_log count.

## 5. Reviews (both mandatory passes; quoted verbatim)

After the reviews:
- the code and test comments were corrected to state the real residual (code review Medium 1);
- the proposed follow-ups are recorded in §6.

### 5a. Code review
> ## B-242 InviteUser review (option 2)
>
> **Verdict:** The change is correct and safe to land. There are no High findings. The Medium findings are about how accurately the change is described, plus a UI gap next to it. Neither blocks the backend change.
>
> ### Commands run (read-only; no DB tests, no file changes)
> - `git diff HEAD -- eami-api` and `cat invite_enumeration_pg_test.go`
> - sed/grep over `users.go`, `agents.go` (`isUniqueViolation`), `workspaces_pg_test.go` (`newWorkspaceTestEnv`), `workflows_test.go` (`seedTestOrg`/`seedTestUser`), `cmdb_fixup_pg_test.go` (`expectCMDBStatus`), `middleware.go` (`writeError`), `store/users.sql.go`, `store/auth.sql.go`, `schema/migrations-v2/000001_baseline.up.sql`, `api/openapi.yaml`, `eami-ui/src/hooks/useUsers.ts`, `eami-ui/src/pages/settings/SettingsPage.tsx`, `eami-ui/src/lib/query.ts`
> - `go build ./...` passed. `go vet ./internal/api/` passed, and it compiles the new test file.
> - `gofmt -l`: `users.go` is flagged, but only for CRLF line endings. The HEAD blob is flagged the same way, so this predates the change.
>
> ### Findings
>
> **Medium 1: the claim that the leak is "narrowed" is most likely wrong.** Locations: `eami-api/internal/api/users.go:186-190` (comment) and the test's doc comment at `invite_enumeration_pg_test.go:12-19`.
> - `users` has two unique constraints: `email TEXT NOT NULL UNIQUE` (baseline.up.sql:46) and `UNIQUE (org_id, email)` (baseline.up.sql:58).
> - Postgres checks unique indexes in OID order. The column-level `users_email_key` is created before the table-level one, so both same-org and cross-org duplicates almost certainly failed on `users_email_key` before this change.
> - `pgconn.PgError.Error()` leaves out `Detail`, so the old 500 did not echo the email either.
> - So before the change, same-org and cross-org already got byte-identical responses. The only difference was 500 with constraint text versus 201.
> - What actually changed: DB internals are no longer disclosed, the status is now a proper 409, and nothing in-band distinguishes the two cases any more. The existence oracle ("does this email exist") carries exactly as much information as before.
> - Separately, an admin can call `GET /v1/users` (`ListUsers`, which lists their own org's live users). A 409 for an email that isn't in that list tells them it belongs to another org, or to a soft-deleted account in their own org. That reveals cross-org registration anyway.
> - Suggestion: reword the code comment, test comment and BUILT.md/BACKLOG entry to "removes DB error text, standardises on 409; cross-org existence oracle unchanged (still derivable via ListUsers)". That way the deferred real fix isn't under-prioritised. This isn't a code defect.
>
> **Medium 2: the UI shows nothing when the invite fails, including on 409.** Locations: `eami-ui/src/pages/settings/SettingsPage.tsx:228-232` and `eami-ui/src/hooks/useUsers.ts:19-28`.
> - `onInvite` calls `invite.mutateAsync(...)` without try/catch.
> - The hook throws the raw `{code, message}` object, which is not an `Error`.
> - react-hook-form's `handleSubmit` re-throws it as an unhandled rejection.
> - There is no `MutationCache.onError` in `src/lib/query.ts`, and the modal doesn't render `invite.error`.
> - Result: the spinner stops and the modal stays open with no message.
> - This predates the change (the old 500 behaved the same), but the 409 is now a designed, user-facing response.
> - Suggested follow-up B-ID (not in B-242 scope): catch the error and show the 409 message with `showToast(..., { type: 'error' })`. That matches the `onGenerateResetLink` pattern in the same component and the B-182 toast rule. Note the check has to be `err?.message`, not `instanceof Error`.
>
> **Low 1: a revoked user can never be re-invited, and the new message gives no way forward.** Location: `users.go:192-194`.
> - `users.email` is globally unique and revoke is a soft delete, so re-inviting a teammate who was revoked earlier now gets 409 "this email cannot be invited" and no alternative.
> - Before, it was an opaque 500, so this is not a regression. It is a real product dead-end, though, and the new wording hardens it.
> - Worth a QUEUED item, for example an un-revoke/restore path for the same org.
>
> **Low 2: the "no rows written" assertion is effectively tautological.** Location: `invite_enumeration_pg_test.go:62-64`.
> - While the unique constraint exists, the rejected INSERT can't write a user row. And `invite_tokens` can't be written without one, because the insert is inside the same aborted transaction.
> - The assertion only catches a regression if the constraint is dropped, or if someone moves token creation ahead of the user insert.
> - It's cheap and harmless, so keep it, but don't count it as rollback coverage.
>
> **Info 1: correctness and the transaction.**
> - `CreateInvitedUser` is the first statement in the transaction. When it fails, the transaction is aborted, `defer tx.Rollback(ctx)` (users.go:172) cleans up, and no partial rows can exist.
> - `isUniqueViolation` uses `errors.As` on `*pgconn.PgError` with code 23505. That's correct through the store wrapper.
> - The only unique constraints `CreateInvitedUser` can hit are the pkey (server-generated) and the two email constraints, so mapping every 23505 to this message is accurate.
> - The non-unique error path now logs through `slog` and returns a generic 500. Good.
> - Leftover: the `invite_tokens` insert and `Commit` error paths already used fixed messages, so they don't leak DB text either.
>
> **Info 2: the test would catch the regressions you asked about.**
> - DB text leaking: the byte-exact `want` comparison would fail, and the substring check on `users_email_key`/`duplicate`/`SQLSTATE`/`23505`/the email is a backstop.
> - Same-org vs other-org becoming distinguishable: every case is compared to the same fixed string.
> - Soft-deleted other-org: covered.
> - Happy path: still returns 201 with an invite link.
> - Gaps (Info only): no same-org soft-deleted case, and timing side channels aren't tested (both cases go through the same code path, so that's acceptable).
>
> **Info 3: the CLAUDE.md real-Postgres pool rule is followed.**
> - `newWorkspaceTestEnv` (`workspaces_pg_test.go:63`) closes the pool with `t.Cleanup(func() { pool.Close() })`, registered right after Ping and before any seed helper's cleanup.
> - `seedTestOrg`'s `DELETE FROM orgs` is a later `t.Cleanup`, so it runs first (LIFO), before the pool closes.
> - The new test has no plain `defer`.
> - Cleanup of the seeded users and of the fresh invitee (plus its `invite_tokens`) relies on `ON DELETE CASCADE` from `orgs` to `users` to `invite_tokens`. The invitee's `invited_by` points at adminA in the same org, so both rows go in the same cascade and the NO ACTION foreign key doesn't block it. No leaked rows.
>
> **Info 4: the OpenAPI contract** (`api/openapi.yaml:2420`) already lists `'409': User already exists` with the `Error` schema, so status and shape match.
> - Minor wording drift: "User already exists" isn't quite accurate for the cross-org or soft-deleted cases.
> - Unrelated drift found while checking, which predates this change: line 2416 says the invite link "Expires after 72 hours", but `inviteTokenTTL` is 48h (`provisioning.go:60`).
> - Also predating it: the 201 schema documents only `invite_link`, while the handler also returns `user` and `expires_at`.
> - These are for the Architect to decide; I didn't fix them.
>
> **Info 5: idiom.** Returning `writeError(w, http.StatusConflict, "conflict", ...)` on `isUniqueViolation` matches `agents.go:175`, `workspaces.go:156` and `model_pricing.go:124`. The `slog.Error("<handler>: <what> failed", "org_id", ..., "err", err)` call matches `cmdb.go:50` and `agents.go:698`. Pulling the message into a named const is fine.
>
> **Info 6: related issue outside this change.** Email uniqueness and `GetUserByEmail` are case-sensitive (exact `=`, not citext/lower). So `Victim@corp.com` and `victim@corp.com` can both be created as separate accounts, and the 409 only fires on an exact-string match. Worth logging in NOTES.md or BACKLOG, not fixing here.
>
> **Info 7: scope.** The diff is limited to `InviteUser`'s error branch, the `log/slog` import, the new const, and the one new test file. No unrelated edits in `eami-api`.
>
> ### Relevant paths
> - C:\AI\EAIM\eaim\eami-api\internal\api\users.go
> - C:\AI\EAIM\eaim\eami-api\internal\api\invite_enumeration_pg_test.go
> - C:\AI\EAIM\eaim\eami-api\internal\api\workspaces_pg_test.go
> - C:\AI\EAIM\eaim\schema\migrations-v2\000001_baseline.up.sql
> - C:\AI\EAIM\eaim\api\openapi.yaml
> - C:\AI\EAIM\eaim\eami-ui\src\pages\settings\SettingsPage.tsx
> - C:\AI\EAIM\eaim\eami-ui\src\hooks\useUsers.ts

### 5b. Security review
> ## B-242 option 2 security review (InviteUser uniform 409)
>
> **Verdict: the change is correct and does what it claims.** Inside InviteUser, a cross-org conflict now looks the same as a same-org conflict. Status, body, headers and timing all match, no DB text leaks, and the transaction rolls back cleanly. The residual is larger than "exists somewhere", though. Once the response is combined with ListUsers and with Login's existing oracles (below), an attacking admin learns considerably more. None of the findings below blocks this change. They are pre-existing, and they are listed for the founder.
>
> **What I ran:** `go build ./...` and `go vet ./internal/api/` (both clean; vet includes the new `_test.go`), plus `git diff HEAD -- eami-api` and read-only greps and reads of `users.go`, `auth.go`, `provisioning.go`, `bootstrap.go`, `workspaces.go`, `ratelimit_login.go`, `store/users.sql.go`, `store/auth.sql.go` and `schema/migrations-v2/000001_baseline.up.sql`. I ran no DB tests and changed no files.
>
> ### 1. Is cross-org now indistinguishable from same-org?
> - **Status, body and headers match.** Every `isUniqueViolation` (23505) takes the same branch (`users.go:192-194`) and goes through the shared `writeError`, so the headers are the same. The only unique constraints on `users` are the random UUID primary key, `email` and `(org_id,email)` (`000001_baseline.up.sql:46,58`), so 23505 can only mean an email clash. Which constraint fired is never exposed. The `slog.Error` at `users.go:196` only runs for non-unique errors, so logging doesn't differ between the cases.
> - **Timing matches.** Both cases run the same single INSERT and fail on the same unique-index check. Any difference is sub-millisecond noise. One Info-level edge: if another transaction is inserting the same email concurrently and hasn't committed, the INSERT blocks until it does. That is not a practical oracle.
> - **Subtraction residual (Info, a documentation fix).** Same-org *active* users are already visible through `GET /v1/users`. `ListUsers` filters `deleted_at IS NULL` (`store/users.sql.go:26-28`). So a 409 for an email the admin doesn't see in their own list means one of two things:
>   - an account of any state (active, pending invite or soft-deleted) in some other org, or
>   - a soft-deleted account in the admin's own org.
>
>   The accurate residual is therefore: **"a 409 on an email that is not in your own active user list shows that the exact byte-string email is registered in another tenant, unless it is one of your own org's deactivated users."** The endpoint is admin-only but has no rate limit, so an admin can check a candidate list in bulk.
> - **The residual grows when combined with Login (see F2 and F3).** Once an admin has a 409, the unauthenticated Login endpoint tells them the account's *state*:
>   - a fast 401 with "invalid email or password" means soft-deleted;
>   - a 401 with "account uses SSO" means a pending, unaccepted invite or an SSO account;
>   - a slow 401 (bcrypt runs) means an active password account.
> - **Side effect (Info).** A soft-deleted user can never be re-invited, even into their own org, because `DeleteUser` is a soft delete and uniqueness is global. This is functional, not a leak.
>
> ### 2. Other routes that touch `users.email`
>
> | # | Sev | Route | Finding |
> |---|---|---|---|
> | F1 | **Medium** | Global `email UNIQUE` (`000001_baseline.up.sql:46`), exploited through InviteUser | **Cross-tenant email squatting / denial of service.** Any org admin can pre-invite `alice@victimcorp.com`. After that, the real tenant can never invite that exact string: it gets the new 409. The squatting admin also receives the invite link (`users.go` response) and can accept it, so they hold a working account under the victim's email address. Nothing verifies email ownership. Out-of-band email delivery (the deferred real fix) is what closes this. It predates B-242. |
> | F2 | **Medium** | `POST /v1/auth/login` (`auth.go:37-44`) | **Explicit body oracle, unauthenticated.** An account with `password_hash IS NULL` returns `401 "account uses SSO"`. An unknown email returns `401 "invalid email or password"`. Every invited but not-yet-accepted user has a NULL hash, so anyone on the internet can confirm a pending invite exists, without any timing measurement. Those are exactly the users whose invite links are still live. The per-IP and per-account limiters (`ratelimit_login.go`) slow this down but don't stop it. Fix: return the same generic message and run a dummy bcrypt. |
> | F3 | **Medium** | `POST /v1/auth/login` (`auth.go:37-40` vs `auth.go:75`) | **Timing oracle, unauthenticated.** An unknown email (or a soft-deleted one, since `GetUserByEmail` filters `deleted_at IS NULL`, `store/auth.sql.go:35`) returns 401 immediately after one indexed lookup. An existing password account runs `bcrypt.CompareHashAndPassword` at cost 12 (`auth/auth.go:123,133`). That is hundreds of milliseconds, easy to measure over a network with a few samples. The rate limiters slow it but don't stop it. Fix: compare against a fixed dummy hash when the lookup misses or the hash is NULL. I did not measure bcrypt here; the cost factor is taken from source. |
> | F4 | Info | `POST /v1/auth/request-reset` (`provisioning.go:225-253`) | The response is always `200 {"status":"ok"}`, and the lookup runs whether or not the email exists. The only difference is a `log.Printf` on a hit (microseconds). The IP limiter runs before any lookup. Not a practical oracle. |
> | F5 | Info | `POST /v1/auth/accept-invite` (`provisioning.go:94-201`) | Keyed on the token, not the email. It returns the email only to the token holder, so it is not an enumeration vector. The used / expired / already-accepted messages are about the token, not the email. |
> | F6 | Info | `POST /v1/setup/bootstrap` (`bootstrap.go:169,252,268-271`) | Requires a single-use setup token and only works before the appliance is configured (409 `already_configured` after that). A unique violation on the admin email returns a generic 500 "could not create admin user" with no DB text. Not an oracle. |
> | F7 | Info | `PATCH /v1/users/me` (`users.go:310+`) | Only `Name` can be changed, and there is no email update path. Not a vector. |
> | F8 | Info | `POST /v1/workspaces/{id}/members` (`workspaces.go:466-494`), `POST /v1/users/{id}/reset-link` (`provisioning.go:269-295`) | Keyed on user UUID and scoped to the caller's org (404 on another org's user). The reset-link endpoint's `400 "account uses SSO"` only applies within the caller's own org. Not a cross-org oracle. |
>
> ### 3. Rollback and partial rows
> **No partial rows or tokens are left (Pass).** Before any write, `defer tx.Rollback(ctx)` is registered (`users.go:175`). The user INSERT and the `invite_tokens` INSERT run in the same transaction. The failure path returns before `Commit`, so the aborted transaction is rolled back. The raw token is generated before the transaction but only in memory, and it is discarded. Nothing is persisted or returned. The new test checks that the `users` and `invite_tokens` row counts don't change for all three conflict cases. One Info-level note: the rollback uses the request context. If the client disconnects, pgx fails the rollback and closes the connection, and Postgres still aborts the transaction, so it stays safe.
>
> ### 4. Case sensitivity and confusable duplicates
> - **Low (Medium once SSO or email linking arrives): matching is case- and whitespace-sensitive, with no normalization.**
>   - `email` is plain `TEXT NOT NULL UNIQUE`. There is no `citext`, no `lower(email)` unique index and no normalizing trigger (`000001_baseline.up.sql:46,58`). The only `lower(` in the migrations is for CMDB names.
>   - InviteUser passes `req.Email` straight through with no `TrimSpace` or `ToLower` (`users.go:125,180`).
>   - Login looks up with an exact `WHERE email = $1` (`store/auth.sql.go:35`). The comment at `ratelimit_login.go:66-71` already acknowledges this.
> - **Consequences:**
>   - `Alice@x.com`, `alice@x.com` and `alice@x.com ` (trailing space) can all exist as separate accounts, in the same org or different ones. That gives confusable identities in user lists and in the approval `decided_by` field (`approvals.go:184` uses the email from the JWT).
>   - It also undercuts F1's workaround: a squatted tenant can get around the block with a case variant, but only by creating a confusable duplicate.
>   - The existence oracle only matches exact bytes, which makes guessing slightly harder but is not a defence.
>   - Bootstrap and request-reset trim whitespace (`bootstrap.go:169`, `provisioning.go:240`), but invite and login don't, so a user invited as `"a@x "` can't be found through request-reset.
> - **Not directly exploitable today:** login still needs the password of the exact-case account, and nothing auto-links accounts by email (I found no SSO/OIDC routes in `router.go`). It becomes important as soon as SSO, email delivery or email-based account linking is added.
> - **Recommendation:** normalize with `lower(trim())` on write and on lookup, and add a unique index on `lower(email)`. That needs a migration plus a dedup check of existing rows, so it is out of B-242's scope and would need its own B-ID.
>
> ### Suggested residual wording for BUILT.md / BACKLOG.md
> "InviteUser's 409 is identical for same-org, cross-org and soft-deleted conflicts. An org admin can still learn that an exact-case email is registered in another tenant (an email not in their active user list that returns 409), in bulk and without a rate limit. Combined with Login's pre-existing 'account uses SSO' message and bcrypt timing, they can also tell whether that account is pending, active or deleted. Email is not normalized, so case variants are separate accounts. Closing this fully needs out-of-band invite delivery, plus separate fixes for the Login oracle and email normalization."

## 6. Proposed follow-ups found by the reviews (pre-existing; not minted, pending the founder)
- **Cross-tenant email squatting** (security F1, Medium). Any org admin can pre-invite `alice@victimcorp.com`, accept their own invite link, and hold a working account under the victim's address. The real tenant can then never invite that email. Nothing verifies email ownership; option 1 (email delivery) closes it.
- **Login enumeration oracles** (security F2 and F3, Medium, unauthenticated). The `"account uses SSO"` message exposes pending invites. bcrypt timing separates existing password accounts from unknown ones. Fix: one generic message plus a dummy bcrypt on a miss or a NULL hash.
- **Email not normalized** (security §4, Low; Medium once SSO or linking arrives). `Alice@x`, `alice@x` and `alice@x ` can each be a separate account. Invite and login don't trim. Fix: `lower(trim())` plus a unique index, which needs a migration and a dedup check.
- **Invite UI shows nothing on failure** (code review Medium 2). `SettingsPage.tsx` `onInvite` has no catch, so the new 409, like the old 500, silently leaves the modal open. Fix: `showToast(err.message, {type:'error'})` (B-182 pattern).
- **A revoked user can never be re-invited** (code review Low 1). Revoke is a soft delete and email uniqueness is global, so it needs a same-org restore path.
- **OpenAPI drift** (code review Info 4, Architect-owned):
  - the 409 description says "User already exists";
  - the spec says the invite link lasts 72 h, but the code uses 48 h;
  - the 201 schema omits `user` and `expires_at`.
