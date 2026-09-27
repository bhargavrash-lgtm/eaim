# B-238 Verification Record — Slack webhook sends bypassed the SSRF dial guard

Written 2026-09-27 by Claude Code. The issue was found by the org-ownership sweep (endpoint 55) and reported immediately. The founder then issued an urgent fix brief.

**Checkability.** Raw logs are in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad:
- `ssrf_live.log`: the pre-fix route attack;
- `b238_listener_hits.log`: the host listener;
- `b238_live.log`, `b238_live_final.log`: the post-fix live runs;
- `b238_mutation.log`: the mutation runs;
- `b238_api_full3.log`: the full test suite on the final code;
- `b238_before.txt`, `b238_after.txt`: the cleanup snapshots.

The reviews in §5 are the subagents' hand-back reports, extracted verbatim from the session transcript.

## 1. The bug

`TestNotificationChannel` (`api/settings.go`) and the alert engine's `SendSlack` (`alerting/dispatcher.go`) sent POST requests with a bare `http.Post` (Go's default client) to the org-admin-set `slack_webhook_url`. That client has no SSRF guard, no timeout, follows redirects, and honours environment proxies. `UpdateNotificationConfig` stored any URL, and the test route echoed `err.Error()`.

The sibling outbound paths (`TestTool`, `DiscoverOpenAPI`) already dialled through `safeDialContext`. So this was the sweep's "guard on one path, missing on its sibling" shape.

## 2. The fix

| Part | Change |
|---|---|
| **Shared guard** | New `internal/netguard` package, needed because `api` imports `alerting` and a shared helper would otherwise create an import cycle. It contains:<br>• `DialContext` and `IsBlocked`, moved from `api/tool_connectivity.go`, whose `safeDialContext`/`isBlockedTestTarget` now delegate to them, so TestTool and DiscoverOpenAPI use the identical guard;<br>• `NewHTTPClient(dial, timeout)`: nil proxy, redirects refused, end-to-end timeout. |
| **Guard hardening** (both reviews' Medium) | `IsBlocked` is rewritten on `net/netip`. It adds CGNAT 100.64/10 (Alibaba metadata), 0/8, 198.18/15, 240/4, the documentation and IETF ranges, all multicast, Teredo, site-local and local-use NAT64. It extracts and re-checks the IPv4 embedded in IPv4-mapped, IPv4-compatible, NAT64 (64:ff9b::/96) and 6to4 addresses; a NAT64 or 6to4 route to a *public* IPv4 still passes. |
| **One client for both senders** | `alerting.NewWebhookClient()` wraps netguard's guard with a 10 s timeout. `Engine.webhook` is set by `NewEngine`, and `TestNotificationChannel` builds the same client. `SendSlack(ctx, client, url, msg)` uses a context and caps the response read. |
| **Save-time validation** | `alerting.ValidateWebhookURL`: https only, a host, no userinfo, not localhost, and the host must resolve only to public addresses. Every rejection returns the same 400 `invalid_webhook_url` "webhook URL must be a public https URL", so the save response can't reveal which internal names exist. |
| **Oracle closed** | Every delivery failure on the test route returns the single `{"sent":false,"reason":"webhook_delivery_failed"}`. The cause (blocked address, DNS failure, refused port, non-HTTP service, non-200, timeout, redirect) is logged server-side only. |
| **No secret in logs** (review Medium/Low) | `SendSlack` unwraps `*url.Error`, whose text embeds the full URL (a credential), to `slack webhook <Op>: <cause>`. This fixes both log sites, the route's and the engine's (the engine's existed before this fix). |
| **Housekeeping** (review Lows) | `defer client.CloseIdleConnections()` for the per-request client; the `toolDialOverride` comment notes its second use. |

## 3. Automated verification

```
eami-api: go build ./... ; go vet ./... → clean; gofmt clean on every file this fix wrote (engine.go/engine_test.go were already unformatted at HEAD)
eami-api: POSTGRES_PASSWORD=… go test -count=1 -v ./... → every package ok; top-level PASS=499 FAIL=0 SKIP=0 (was 486 before B-238; +13 new)
eami-gateway: go build ./... → ok (not changed)
```

**New tests:**
- **`internal/netguard/netguard_test.go`:** the dialer blocks loopback, localhost, metadata, RFC1918, ::1 and 0.0.0.0, and a loopback listener accepts 0 connections. Also: redirects are not followed; there is no proxy; the timeout fires; and the special-range/embedded-IPv4 table.
- **`internal/alerting/webhook_test.go`:**
  - the ValidateWebhookURL table: 18 rejected forms, and a public https URL accepted;
  - the production client never reaches a loopback server, while an unrestricted control does;
  - NewEngine wires the guarded client (hermetic);
  - SendSlack errors never contain the URL.
- **`internal/alerting/engine_webhook_ssrf_pg_test.go`** (real Postgres, the real `evaluateRule` path): a firing rule with a loopback webhook creates the alert with notified=false and 0 hits. The unguarded control gets notified=true and 1 hit.
- **`internal/api/notification_webhook_ssrf_pg_test.go`** (real Postgres, full HTTP):
  - **Save:** the exact live attack URLs (plus https forms) give a byte-identical 400, and nothing is stored. A public https URL saves, and "" clears it.
  - **Send**, with URLs planted in the DB: open loopback port, closed port, metadata IP, `postgres` and an unresolvable host all return a byte-identical body with 0 hits.
  - **Unguarded control:** the send is delivered, and a redirect is not followed.

**Mutation checks.** Each part is broken on its own (`b238_mutation.log`; failing tests listed, file:line detail in the log):
```
=== M1: every part of the fix removed at once (pre-fix behaviour: unguarded client, no save validation, err.Error() echoed)
ok  	github.com/eami/api/internal/netguard	1.482s
--- FAIL: TestEngineSlackDispatch_InternalTargetBlocked_RealDB (0.10s)
--- FAIL: TestValidateWebhookURL (0.00s)
--- FAIL: TestSendSlack_ProductionClientBlocksInternalTargets (0.00s)
--- FAIL: TestNewEngine_UsesGuardedWebhookClient (0.00s)
FAIL	github.com/eami/api/internal/alerting	0.292s
--- FAIL: TestNotificationWebhook_SaveRejectsInternalTargets_RealDB (0.16s)
--- FAIL: TestNotificationWebhook_TestSendBlockedAndUniform_RealDB (0.06s)
--- FAIL: TestNotificationWebhook_UnguardedControlDelivers_RealDB (0.06s)
FAIL	github.com/eami/api/internal/api	0.467s
=== M2: SSRF guard removed from the shared webhook client only
ok  	github.com/eami/api/internal/netguard	1.529s
--- FAIL: TestEngineSlackDispatch_InternalTargetBlocked_RealDB (0.10s)
--- FAIL: TestSendSlack_ProductionClientBlocksInternalTargets (0.00s)
--- FAIL: TestNewEngine_UsesGuardedWebhookClient (0.00s)
FAIL	github.com/eami/api/internal/alerting	0.347s
--- FAIL: TestNotificationWebhook_TestSendBlockedAndUniform_RealDB (0.06s)
FAIL	github.com/eami/api/internal/api	0.565s
=== M3: save-time URL validation removed only
ok  	github.com/eami/api/internal/netguard	1.388s
ok  	github.com/eami/api/internal/alerting	0.285s
--- FAIL: TestNotificationWebhook_SaveRejectsInternalTargets_RealDB (0.16s)
FAIL	github.com/eami/api/internal/api	3.309s
=== M4: uniform failure reason removed only (raw err.Error() echoed; guard kept)
ok  	github.com/eami/api/internal/netguard	1.716s
ok  	github.com/eami/api/internal/alerting	0.415s
--- FAIL: TestNotificationWebhook_TestSendBlockedAndUniform_RealDB (0.05s)
--- FAIL: TestNotificationWebhook_UnguardedControlDelivers_RealDB (0.05s)
FAIL	github.com/eami/api/internal/api	0.529s
=== M5: alert engine path only sends with an unguarded client (route kept guarded)
ok  	github.com/eami/api/internal/netguard	1.460s
--- FAIL: TestEngineSlackDispatch_InternalTargetBlocked_RealDB (0.09s)
--- FAIL: TestNewEngine_UsesGuardedWebhookClient (0.00s)
FAIL	github.com/eami/api/internal/alerting	0.258s
ok  	github.com/eami/api/internal/api	3.347s
=== M6: redirect refusal removed from the guarded client only
--- FAIL: TestNewHTTPClient_DoesNotFollowRedirects (0.00s)
FAIL	github.com/eami/api/internal/netguard	1.468s
ok  	github.com/eami/api/internal/alerting	0.318s
--- FAIL: TestNotificationWebhook_UnguardedControlDelivers_RealDB (0.08s)
FAIL	github.com/eami/api/internal/api	3.235s
=== M7: guard blocklist reverted to the standard-library classes only (review Medium)
--- FAIL: TestIsBlocked_SpecialRangesAndEmbeddedIPv4 (0.00s)
FAIL	github.com/eami/api/internal/netguard	1.513s
ok  	github.com/eami/api/internal/alerting	0.295s
ok  	github.com/eami/api/internal/api	3.329s
=== M8: webhook URL redaction removed from SendSlack errors (review Medium/Low: secret in logs)
ok  	github.com/eami/api/internal/netguard	1.553s
--- FAIL: TestSendSlack_ErrorNeverContainsURL (0.00s)
FAIL	github.com/eami/api/internal/alerting	0.293s
ok  	github.com/eami/api/internal/api	3.372s
=== control: sources restored
ok  	github.com/eami/api/internal/netguard	1.547s
ok  	github.com/eami/api/internal/alerting	0.447s
ok  	github.com/eami/api/internal/api	3.534s
```
Each part of the fix is caught when removed on its own, and M1 (everything removed) is caught by 7 tests.

## 4. Live verification (shared Docker stack)

**Pre-fix, route** (the sweep's report; `ssrf_live.log`, verbatim):
```
webhook=http://127.0.0.1:8081/health                       save=200 test=200 21ms {"sent":false,"reason":"slack_webhook_non_200"}
webhook=http://127.0.0.1:1/                                save=200 test=200 15ms {"sent":false,"reason":"Post \"http://127.0.0.1:1/\": dial tcp 127.0.0.1:1: connect: connection refused"}
webhook=http://postgres:5432/                              save=200 test=200 11ms {"sent":false,"reason":"Post \"http://postgres:5432/\": EOF"}
webhook=http://eami-gateway:8080/health                    save=200 test=200 20ms {"sent":false,"reason":"slack_webhook_non_200"}
webhook=http://169.254.169.254/latest/meta-data/           save=200 test=200 9ms {"sent":false,"reason":"Post \"http://169.254.169.254/latest/meta-data/\": dial tcp 169.254.169.254:80: connect: connection refused"}
```

**Pre-fix, real alert-engine path** (reproduced for this brief on the still-running pre-fix container). A throwaway org had its webhook planted as `http://host.docker.internal:18238/engine-prefix`; that host resolves to the private address 192.168.65.254 from inside the container. A host listener recorded the request, and the alert was marked `notified=true`:
```
2026-09-27T16:46:18.987Z POST /engine-prefix from=127.0.0.1 body={"text":":warning: *EAMI Alert — b238-engine-prefix*\n\u003e denied_actions_count = 0.00 (threshold: -1, window: 5m)\n\u003e Severity: *warning*"}
```

**Post-fix** (`b238_live_final.log`, the final build; the API container was created 2026-09-27T16:53:11Z, verbatim):
```
listener hits before this run: 1 (the pre-fix engine reproduction)
── (1) save time: every attack URL rejected with one identical body, nothing stored
PUT webhook=http://127.0.0.1:8081/health                         400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=http://127.0.0.1:1/                                  400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=http://postgres:5432/                                400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=http://eami-gateway:8080/health                      400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=http://169.254.169.254/latest/meta-data/             400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=http://host.docker.internal:18238/route              400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://127.0.0.1:8081/health                        400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://169.254.169.254/latest/meta-data/            400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://host.docker.internal:18238/route             400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://postgres/                                    400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://does-not-exist.invalid/                      400 {"code":"invalid_webhook_url","message":"webhook URL must be a public https URL"} (stored URL unchanged)
PUT webhook=https://hooks.slack.com/services/… (public)          200 {"slack_enabled":true,"slack_webhook_url":"***********************************************************************XXXXXX
── (2) send time: attack URLs planted directly in the DB (a pre-fix row), POST /test
test webhook=http://127.0.0.1:8081/health                        200 {"sent":false,"reason":"webhook_delivery_failed"} 10ms
test webhook=http://127.0.0.1:1/                                 200 {"sent":false,"reason":"webhook_delivery_failed"} 7ms
test webhook=http://postgres:5432/                               200 {"sent":false,"reason":"webhook_delivery_failed"} 17ms
test webhook=http://eami-gateway:8080/health                     200 {"sent":false,"reason":"webhook_delivery_failed"} 8ms
test webhook=http://169.254.169.254/latest/meta-data/            200 {"sent":false,"reason":"webhook_delivery_failed"} 11ms
test webhook=http://host.docker.internal:18238/route             200 {"sent":false,"reason":"webhook_delivery_failed"} 13ms
test webhook=https://does-not-exist.invalid/                     200 {"sent":false,"reason":"webhook_delivery_failed"} 42ms
listener hits after route tests: 1 (before: 1)
── (3) real alert-engine path: planted internal URL + a firing rule, wait for the engine tick
engine fired alert (post-fix rule)                               8ca4a69f-e7a8-4984-ab94-54e53a2f63b9 notified=false
pre-fix rule alert, for comparison                               35c4b281-6828-45dd-9ea5-ea6b788a1738 notified=true
listener hits after engine tick: 1 (before this run: 1)
listener log:
2026-09-27T16:46:18.987Z POST /engine-prefix from=127.0.0.1 body={"text":":warning: *EAMI Alert — b238-engine-prefix*\n\u003e denied_actions_count = 0.00 (threshold: -1, window: 5m)\n\u003e Severity: *warning*"}
── (3b) engine path on the FINAL build (API container created 2026-09-27T16:53:11Z), fresh rule b238-engine-final-build
   (step (3) above matched the earlier post-fix alert 8ca4a69f… by rule name, so it is not final-build evidence; this is)
engine fired alert (final-build rule)  : 48cbf9e4-a684-44c9-abbe-9b48b9b90173 fired_at=2026-09-27 16:54:13.589264+00 notified=false
engine log (docker logs eaim-eami-api-1): 2026/09/27 16:54:13 alerting: slack dispatch for rule b238-engine-final-build: slack webhook Post: connections to loopback/link-local/private addresses are not permitted
listener hits before / after           : 1 / 1   (the single hit is the 16:46:18Z pre-fix /engine-prefix delivery)
route log line (URL redacted)          : 2026/09/27 16:53:23 WARN test notification: slack delivery failed org_id=bca1be60-… err="slack webhook Post: connections to loopback/link-local/private addresses are not permitted"
```
- **Save:** every attack URL, including the pre-fix list, `host.docker.internal`, and the https forms, gets one identical 400, and the stored URL is unchanged. A real `https://hooks.slack.com/…` URL still saves.
- **Send**, with each URL planted in the DB as a pre-fix row would be: every target returns a byte-identical `webhook_delivery_failed` in 7–42 ms. There is no open, closed, non-HTTP or metadata distinction left, and the listener count is unchanged.
- **Engine:** the real alert engine fired on the final build and was refused (`notified=false`). The listener stayed at its single pre-fix hit.
- **Logs:** `docker logs` for the window contain 0 occurrences of the webhook URL.
- **Correction, disclosed:** step (3) of that run matched an earlier post-fix alert by rule name. Step (3b) is the final-build evidence, taken with a fresh rule.
- **Positive path:** the "sent: true" path was not exercised live, because that would post to a real third-party Slack workspace. It is covered by the unguarded-control tests. The security reviewer separately confirmed that the guarded client completes TLS to the real `hooks.slack.com`, which returned Slack's own 404 `no_team` for a fake token.

**Cleanup.** The throwaway org, admin user, notification config, 4 rules and their alerts were deleted (cascade), and the fixture password file was removed. `diff b238_before.txt b238_after.txt` is **identical**. The snapshot covers org, user, agent, config, lifecycle, approval, endpoint, tool, workspace, api_key, episode, token-event, notification_config, alert_rules and alerts, plus the **audit_log count**, so there is no residue at all this time. The host listener was stopped.

## 5. Reviews (both mandatory passes completed; quoted verbatim)

Post-review changes:
- **Guard blocklist hardening:** both reviews' Medium.
- **`*url.Error` unwrapping:** the code review's Medium and the security review's Low.
- **Test-client idle-connection close and `toolDialOverride` comment:** code-review Lows.
- **A hermetic NewEngine wiring test:** code-review L3.

The security reviewer then re-reviewed that delta (§5c).

**After the re-review:**
- **Its one Low is fixed.** SIIT IPv4-translated addresses (`::ffff:0:a.b.c.d`, prefix `::ffff:0:0:0/96`) are now unwrapped and checked, with test rows added.
- **Disclosed:** my first attempt used `::ffff:0:0/96`. `netip` parses that as the IPv4-mapped prefix, and the new test rows caught it.
- **Full suite re-run:** 499/0/0 (`b238_api_full3.log`).
- **Live evidence:** the §4 live cycle ran on the build just before this one-line, netguard-only change. The stack was then rebuilt with it (API container created 2026-09-27T17:04:06Z, `/health` ok). No live re-probe was run for that change; its unit rows cover it.

### 5a. Code review
> ## B-238 review: webhook SSRF fix in eami-api
>
> **Verdict:** the fix is sound. No High findings. Every send to the Slack webhook URL now goes through the netguard dialer, and I found no unguarded path left. The moved guard is behaviour-identical for TestTool and DiscoverOpenAPI. One Medium finding: the new log line writes the secret webhook URL to the log.
>
> ### Commands I ran (in `C:\AI\EAIM\eaim\eami-api`, with Go put on PATH)
> - `git diff -- .`, `cat` of all 5 untracked files, and greps for `http.(Post|Get|Head|PostForm)(`, `http.DefaultClient`, `&http.Client`, `http.Client{`, `SendSlack`, `toolDialOverride` and `SlackWebhookURL` across the module.
> - `go build ./...` and `go vet ./...`: both clean (exit 0).
> - `go test ./internal/netguard/ ./internal/alerting/ -run 'TestDialContext|TestNewHTTPClient|TestValidateWebhookURL|TestSendSlack' -count=1 -v`: all 6 passed.
> - `go test ./internal/api/ -run 'SafeDial|IsBlocked|isBlocked|safeDial|TestTool' -count=1`: ok.
> - For both test commands, `TEST_DATABASE_URL` and `POSTGRES_PASSWORD` were unset, so no database was touched. I did not modify any file.
>
> ### Unguarded-path sweep
> - **Webhook URL:** the only consumers are `alerting.SendSlack`, called from `engine.go:196` with `e.webhook` and from `settings.go:235` with the guarded client. `NewEngine` (`engine.go:92`) is the only place an `Engine{}` is built, so `webhook` is never nil in production.
> - **Other outbound clients (none can reach the webhook URL):**
>   - `engine.go:97` is the collector client, with an operator-configured URL.
>   - `gateway_episodes.go:100` is the gateway client.
>   - `tool_connectivity.go:224` and `openapidiscovery.go:104` both use `safeDialContext` in production.
> - **Equivalence of the move:** `netguard.DialContext` and `IsBlocked` match the removed code line for line. The only change is that the error is now the named `ErrBlockedAddress`, with the same text. `openapidiscovery_test.go:387` matches on "not permitted" and still passes. `classifyHTTPError` does not depend on the text.
>
> ### Medium
>
> **M1. The full secret webhook URL is logged. `settings.go:236`, with the root cause in `dispatcher.go:89` (`client.Do` return).**
> - `client.Do` returns a `*url.Error`. Its `Error()` embeds the full request URL, for example `Post "https://hooks.slack.com/services/T0/B0/xxxxSECRET": dial tcp ...: i/o timeout`.
> - The new `slog.Warn(..., "err", err)` therefore writes the credential into server logs on every DNS failure, dial failure, timeout or blocked address.
> - `api/openapi.yaml` (NotificationConfig) says: "Full webhook URL values must never be logged or returned in API responses."
> - The same leak already existed at `engine.go:197` (`log.Printf(... %v, err)`). It happened before this change too, because `http.Post` also returned `*url.Error`. This change adds a second log site with the same leak.
> - **Failure scenario:** a Slack DNS blip or a temporary block writes working webhook tokens into whatever log sink eami-api ships to.
> - **Fix:** in `SendSlack`, unwrap before returning: `var ue *url.Error; if errors.As(err, &ue) { return fmt.Errorf("slack webhook %s: %w", ue.Op, ue.Err) }`. That fixes both call sites. Add a unit test asserting that the returned error does not contain the URL path.
>
> ### Low
>
> **L1. Per-request Transport with keep-alives. `settings.go:231-233`.**
> - `TestNotificationChannel` builds a new `http.Transport` on every call via `NewWebhookClient()` and never calls `CloseIdleConnections`. After a successful send, the connection and its readLoop/writeLoop goroutines sit idle in an unreachable pool until `IdleConnTimeout` (30s).
> - `tool_connectivity.go:218-224` explicitly uses `DisableKeepAlives: true` for this exact reason, so this goes against local idiom.
> - **Scenario:** an admin clicking "Test" repeatedly accumulates up to 30s of idle connections per click. The count is bounded, but it is avoidable churn.
> - **Fix:** add `defer client.CloseIdleConnections()` in the handler, or build the client once on `Server`.
>
> **L2. Stale comment on the reused test hook. `router.go:36-42`.**
> - `toolDialOverride` now also redirects the Slack test send (`settings.go:232`), but its comment still says it is only for TestTool connectivity checks.
> - **Scenario:** a future maintainer changes the hook for tool tests and silently changes the webhook control test too.
> - **Fix:** add one line to the comment mentioning TestNotificationChannel, or add a dedicated `webhookDialOverride` field.
>
> **L3. The engine wiring is only proved by a real-Postgres test.**
> - The hermetic `webhook_test.go` proves that `NewWebhookClient()` blocks loopback. Nothing without a database proves that `NewEngine` wires it in, or that the settings handler uses the fixed reason and runs save-time validation.
> - **Scenario:** if `engine.go:92` were reverted to `&Engine{queries: queries}` with `http.DefaultClient`, or `settings.go` echoed `err.Error()` again, only the `_RealDB` tests would fail. They skip silently when the environment variables are unset.
> - **Fix (optional):** add a trivial hermetic assertion, such as `NewEngine(nil, "", "").webhook.Transport.(*http.Transport).Proxy == nil` plus a loopback `SendSlack` through `e.webhook`. Otherwise, confirm that CI runs the `_RealDB` tests.
>
> **L4. Gaps in the shared guard's blocklist (pre-existing, now reachable from one more path). `netguard.go:39-42`.**
> `IsBlocked` does not cover:
> - 100.64.0.0/10 (CGNAT; Alibaba Cloud metadata is 100.100.100.200);
> - 0.0.0.0/8 addresses other than 0.0.0.0;
> - 198.18.0.0/15;
> - multicast and broadcast (255.255.255.255);
> - deprecated IPv6 site-local fec0::/10;
> - IPv6 NAT64 (64:ff9b::/96) and 6to4 (2002::/16) addresses that embed a private IPv4 address.
>
> - **Scenario:** on a CGNAT-addressed or NAT64 cloud network, an admin-set webhook can reach an internal service.
> - **Fix:** add these ranges to `IsBlocked` with table tests, in a separate B-ID. That is in keeping with a "moved verbatim" change, so it should not be folded into B-238.
>
> ### Test effectiveness: would each part of the fix be caught if removed?
>
> | Part of the fix removed | Caught by |
> |---|---|
> | `NewWebhookClient` dials without netguard | `TestSendSlack_ProductionClientBlocksInternalTargets` (hermetic). Also `TestEngineSlackDispatch_...RealDB` and `TestNotificationWebhook_TestSendBlockedAndUniform_RealDB`. |
> | Redirects followed (`CheckRedirect` removed) | `TestNewHTTPClient_DoesNotFollowRedirects` (hermetic). The API redirect control test would still pass, because the unrestricted dialer returns the 302 as non-200 only when redirects are refused, so it is also a guard. |
> | Timeout removed | `TestNewHTTPClient_TimesOut` |
> | Proxy set to `ProxyFromEnvironment` | `TestNewHTTPClient_NoProxyAndTimeout`. Note that `Proxy: nil` on its own is the zero value, so the explicit field documents intent rather than changing behaviour. |
> | Save-time validation removed | `TestNotificationWebhook_SaveRejectsInternalTargets_RealDB` only (DB-gated) |
> | `err.Error()` echoed as the reason | `TestSendBlockedAndUniform_RealDB`, which compares the exact body across open port, closed port, metadata IP and unresolvable host (DB-gated) |
> | `engine.webhook` not used | `TestEngineSlackDispatch_...RealDB` (DB-gated), with a positive control that swaps in the unrestricted dialer |
>
> Each negative test is paired with a positive control, so a zero hit count cannot be explained by an unreachable server. Good design.
>
> ### Postgres test rule (CLAUDE.md)
> Both files comply: `engine_webhook_ssrf_pg_test.go:44` and `notification_webhook_ssrf_pg_test.go:58`.
> - `t.Cleanup(func() { pool.Close() })` is registered straight after Ping and before the org DELETE `t.Cleanup`.
> - There is no plain `defer pool.Close()` in either file.
> - Child rows cascade from the org DELETE, as the tests' comments say.
>
> The skip paths leak the pool. `pool.Ping` fails, then `t.Skipf` runs before the Close cleanup is registered. This is the same as the template helpers and only matters when Ping fails. Info only.
>
> ### Info
> - **I1. Client and contract drift (pre-existing, outside B-238).** `openapi.yaml` declares the test-route response as `{success, error}`, and `SettingsPage.tsx` `onTest` reads `result.success` and `result.error`. The handler returns `{sent, reason}`, so the UI shows "Test sent." as an error-styled toast either way. The new `webhook_delivery_failed` reason (which replaces `slack_webhook_non_200`) and the 400 `invalid_webhook_url` code are also not in the spec. The existing 400 response does cover the latter generically. The spec also says the mask shows "first 8 characters", but `maskSecret` shows the last 6. The contract is Architect-owned, so this needs its own B-ID.
> - **I2. Stricter option.** `ValidateWebhookURL` could allowlist `hooks.slack.com` rather than "any public https", because the feature is Slack-only. That is a product decision, and the current behaviour is defensible.
> - **I3. Error handling and idiom are otherwise good.**
>   - `writeError` is used with a fixed code and message.
>   - `slog` matches the api package (9 files use slog, 2 use `log`).
>   - `http.NewRequestWithContext` replaces `//nolint:noctx`.
>   - Reading the response body is now capped at 64 KiB.
>   - `ValidateWebhookURL` returns a single sentinel error, so the save response is not an oracle.
>   - The remaining timing difference (DNS lookup versus an immediate block) is negligible.
> - **I4. Scope is appropriate.** Pulling the guard into `internal/netguard` is necessary to avoid an import cycle, since api imports alerting. `tool_connectivity.go`'s doc comment lost one sentence of rationale ("a real boundary crossing ... not merely a restatement"); that is harmless. There is no unrelated refactoring.

### 5b. Security review
> # B-238 SSRF fix: independent review
>
> ## Verdict
> **The fix closes B-238 as reported. No High findings.** Loopback, RFC1918, link-local, cloud-metadata IPs (169.254.169.254 and AWS IPv6 fd00:ec2::254), compose-internal hostnames, redirects, env proxies, DNS rebinding and the err.Error() port-scan oracle are all closed on both send paths: the test endpoint and the alert engine. What remains is one Medium blocklist gap that depends on the deployment, plus some Low and Info items. The positive path works.
>
> ## What I ran (throwaway programs in the scratchpad, outside the repo)
> - `go build ./...` and `go vet` on `./internal/netguard/`, `./internal/alerting/` and `./internal/api/` in eami-api: both clean.
> - **Probe 1:**
>   - Ran `IsBlocked`, copied verbatim, over about 26 special addresses.
>   - Ran `url.Parse`, `Hostname()`, `ParseIP` and `LookupIPAddr` over numeric, IDN, trailing-dot and userinfo URL forms.
>   - Captured the addr that `http.Transport` actually hands the dialer.
> - **Probe 2:**
>   - Copied `netguard.go` verbatim and replicated `ValidateWebhookURL` and `SendSlack`.
>   - Sent a POST to a real `https://hooks.slack.com/services/T.../B.../X...` URL, plus loopback, NXDOMAIN, blackhole and closed-port targets, with timings.
> - **Caveat:** the resolver results come from the Windows resolver. Production (Linux, pure Go or glibc) may resolve numeric forms differently, for example glibc turns `2130706433` into 127.0.0.1. Both save and dial check the resolved IPs, not the string, so the result is the same either way.
>
> ## Verified as not bypasses
> - **IPv4-mapped IPv6:** `::ffff:127.0.0.1`, `::ffff:7f00:1`, `::ffff:169.254.169.254` and `::ffff:10.0.0.1` are all blocked (probe 1). net.IP's `To4()` unwraps them.
> - **Numeric, octal, hex, short, full-width and trailing-dot hosts:**
>   - `2130706433`, `0x7f.1`, `017700000001`, `127.1` and `0177.0.0.1` did not resolve on Windows, so they are rejected.
>   - `127.0.0.1.`, `localhost.` and `１２７.０.０.１` resolve to loopback and are blocked.
>   - `ｌｏｃａｌｈｏｓｔ` is rewritten by the Transport's IDNA step to `localhost:443` before dialing, where DialContext resolves and blocks it.
>   - The string check `localhost` / `.localhost` is only defence in depth. It misses `localhost.`, but the IP check catches it.
> - **Userinfo:** `https://evil.com@127.0.0.1/` is rejected (`u.User != nil`). `https://127.0.0.1\@evil.com/` parses with host evil.com, which is harmless.
> - **Zone IPv6** (`fe80::1%eth0`): the lookup returns fe80::1, which is blocked.
> - **DNS rebinding:** `netguard.go:49` DialContext checks and then dials the same resolved IP literals. Save-time and send-time checks are independent, and a host with any blocked record is refused as a whole.
> - **Redirects:** `netguard.go:111` returns `ErrUseLastResponse`, and SendSlack then fails on the non-200. This is covered by an existing unit test.
> - **Env proxy:** `netguard.go:104` sets `Proxy: nil` explicitly.
> - **Oracle on internal targets:** blocked targets fail in about 0 ms before any connect, and the endpoint returns only `webhook_delivery_failed` (`settings.go:238-239`).
> - **Positive path:** POST to the real hooks.slack.com over the guarded client (dialing the resolved IP, with TLS SNI and verification against the hostname) validated in 113 ms. Slack returned its real `HTTP 404 "no_team"` for the fake token in 479 ms. A valid token would get 200.
>
> ## Findings
>
> ### Medium: blocklist misses special ranges and IPv6 prefixes that embed IPv4
> **Where:** `internal/netguard/netguard.go:38-40` (`IsBlocked`). Both `ValidateWebhookURL` and every dial share it, as do TestTool and OpenAPI spec_url.
>
> **Confirmed not blocked (probe 1):**
>
> | Address | Why it matters |
> |---|---|
> | `100.64.0.0/10` (CGNAT) | Includes **100.100.100.200, Alibaba Cloud's metadata service**; also Tailscale and some Kubernetes CNIs |
> | `64:ff9b::/96` and `64:ff9b:1::/48` (NAT64), e.g. `64:ff9b::a9fe:a9fe`, `64:ff9b::a00:5` | Embed IPv4 addresses |
> | `2002::/16` (6to4), e.g. `2002:a9fe:a9fe::1` | Embeds an IPv4 address |
> | `::/96` IPv4-compatible, e.g. `::127.0.0.1` | Embeds an IPv4 address |
> | `0.0.0.0/8` except 0.0.0.0, `198.18.0.0/15`, `240.0.0.0/4`, `255.255.255.255`, `192.0.0.0/24` | Special-purpose ranges; 192.0.0.192 is legacy Oracle metadata |
> | Non-link-local multicast, e.g. `239.1.1.1` | 224.0.0.1 and ff02::1 are blocked |
>
> **Scenario:**
> 1. An org admin controls a domain with an AAAA record `64:ff9b::a00:5`, which passes save and dial.
> 2. If eami-api runs in an IPv6-only or dual-stack VPC whose route table sends 64:ff9b::/96 to a NAT64 gateway (AWS NAT gateway does this and can reach in-VPC IPv4), the POST lands on 10.0.0.5.
> 3. Likewise, an A record of 100.100.100.200 reaches Alibaba metadata if the service is deployed there.
>
> In the current Docker Compose deployment none of these routes exist, so it is not exploitable today. That is why this is Medium, not High.
>
> **Fix:**
> 1. Parse with `netip`, `Unmap()`, and extract the embedded IPv4 for 64:ff9b::/96, 64:ff9b:1::/48, 2002::/16 and ::/96, then re-check it. Simpler: reject those IPv6 prefixes outright.
> 2. Add an explicit deny list from the IANA special-purpose registries: 0/8, 100.64/10, 192.0.0/24, 192.0.2/24, 198.18/15, 198.51.100/24, 203.0.113/24, 240/4, 255.255.255.255, all multicast (`IsMulticast`), 2001:db8::/32 and 2001::/32 (Teredo).
> 3. Stronger for this one feature: allowlist the host (`hooks.slack.com`, optionally configurable) and port 443 in `ValidateWebhookURL` and at send time.
>
> ### Low: the Slack webhook secret is written to server logs
> **Where:** `internal/api/settings.go:238` (new) and `internal/alerting/engine.go:197` (existing).
>
> The `*url.Error` from `client.Do` formats as `Post "https://hooks.slack.com/services/T…/B…/<secret>": …`, so the full webhook token lands in logs. The API itself masks it everywhere except the last 6 characters.
>
> **Scenario:** anyone with log access (log shipper, support staff) can post to any tenant's Slack.
>
> **Fix:** log `urlErr.Err` (errors.As to `*url.Error`) or a redacted `scheme://host`, never the full URL.
>
> ### Low: timing still shows which internal DNS names exist
> **Where:** `internal/alerting/dispatcher.go:45` (`ValidateWebhookURL`) and `netguard.DialContext`.
>
> - Both paths return the same 400 / `webhook_delivery_failed`, but the latency differs:
>   - A name that resolves and is then blocked costs one fast internal lookup (Docker's embedded DNS answers compose names at once).
>   - An NXDOMAIN may take longer (forwarded upstream, search-domain expansion), up to the 5 s lookup timeout.
> - Measured on Windows: blocked IP literal 0 ms, NXDOMAIN 12 ms.
> - **Scenario:** an admin times `PUT https://postgres/`, `https://redis/` and so on to learn which compose service names exist. This reveals no ports or content.
> - **Fix:** accept it as residual risk, or apply the host allowlist from the Medium fix so hostnames are never resolved. Padding to a fixed minimum latency is optional.
>
> ### Low: one tenant can stall alert delivery for all tenants
> **Where:** `internal/alerting/engine.go:143-150,196`.
>
> `evaluateRules` runs sequentially for all orgs in one goroutine. An org whose webhook points at a public blackhole (for example `https://1.1.1.1:1/`, which passes validation) costs the full 10 s `WebhookTimeout` per firing rule. Measured: 10.0 s.
>
> **Scenario:** tenant A creates N rules that fire, delaying every other tenant's alert evaluation by N×10 s per tick. This is strictly better than before the fix (the old `http.Post` had no timeout at all).
>
> **Fix:** dispatch notifications asynchronously with a bounded worker pool or per-org concurrency, or cap total dispatch time per tick.
>
> ### Info
> - **Other outbound paths reachable with tenant-controlled URLs:**
>   - `internal/api/tool_connectivity.go:224` (TestTool REST) and `internal/openapidiscovery/openapidiscovery.go:104-106` (spec_url fetch) both dial only through `safeDialContext`, which now delegates to `netguard.DialContext`.
>   - Their zero-value `http.Transport` has a nil Proxy. They do follow redirects, but every hop is dialed through the guard.
>   - They share the Medium blocklist gaps.
>   - TestTool still returns distinct connected / auth_failed / unreachable results, but only for public targets.
> - **Operator-configured, not tenant-controlled:** `internal/api/gateway_episodes.go:100` (`&http.Client{Timeout: 5s}` to the gateway) and `internal/alerting/engine.go:97` (the collector client). Out of scope.
> - **SMTP:** `email_smtp_host` and `email_smtp_port` are stored with no validation (`settings.go:179-184`) but are never used for sending: the test endpoint returns `smtp_not_configured` and no net/smtp code exists. When email sending is built, it must dial through `netguard.DialContext` and validate on save. Consider a backlog note.
> - **Scheme not enforced at send time:** a URL stored before this fix (for example an `http://` URL or an internal host) stays in the DB. Send time blocks internal addresses, but a legacy `http://public` URL would still send the token in plaintext.
>   - Fix: re-check `https` in `SendSlack`, and/or run a one-off scan of stored `slack_webhook_url` values.
> - **Blind requests to public hosts:** the test endpoint can still POST to any public host and port with EAMI's egress IP, and 10 s timeout vs fast failure reveals public port state.
>   - Fix: restrict to port 443 (or allowlist the host) and rate-limit the test endpoint.
> - **DNS lookups of attacker-chosen names:** saving a URL makes eami-api resolve an attacker-chosen name, which reveals the egress resolver. This is inherent and acceptable.
> - **Idle connections:** `settings.go:231` builds a new client (with keep-alive) per test call and never closes it. Idle connections linger up to 30 s (`IdleConnTimeout`). This is minor; `DisableKeepAlives` or `CloseIdleConnections()` after the send would fix it.
> - **Masked value sent back:** a PUT that sends back the masked GET value (`*****abc123`) now gets a 400 instead of storing garbage. This is an improvement, but the UI should not round-trip the masked value.
>
> ## Files reviewed
> - `C:\AI\EAIM\eaim\eami-api\internal\netguard\netguard.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\alerting\dispatcher.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\alerting\engine.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\settings.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\tool_connectivity.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\openapidiscovery\openapidiscovery.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\gateway_episodes.go`
>
> No repo files were modified.

### 5c. Security re-review of the post-review delta
> # B-238 delta re-review (netguard IsBlocked rewrite, SendSlack error redaction, CloseIdleConnections)
>
> ## Verdict
> **The delta is correct. There are no High or Medium findings.**
> - The embedded-IPv4 offsets are right. I checked them with a program.
> - I found no false positives on real public SaaS addresses.
> - The URL redaction holds on every error path.
>
> One Low gap remains: SIIT's IPv4-translated form `::ffff:0:a.b.c.d` is not unwrapped. The rest are Info.
>
> ## What I ran
> Read-only throughout; no repo edits and no DB tests.
> - **Repo checks:**
>   - `go build ./...` and `go vet ./internal/netguard/ ./internal/alerting/ ./internal/api/` are clean.
>   - `go test ./internal/netguard/` passes. It includes `TestIsBlocked_SpecialRangesAndEmbeddedIPv4`.
>   - `go test -run 'TestValidateWebhookURL|TestSendSlack_|TestNewEngine_UsesGuardedWebhookClient' ./internal/alerting/` passes all 4 hermetic tests. It does not run the `_pg_test` files.
> - **Throwaway program:** `scratchpad/ng2`, outside the repo.
>   - It copies the new `netguard.go` verbatim with `package main`.
>   - It calls `embeddedIPv4` and `IsBlocked` directly on about 50 addresses.
>   - It resolves 15 real public hostnames and checks every returned IP against `IsBlocked`.
>
> ## Correctness of the new `IsBlocked` (`internal/netguard/netguard.go`)
>
> **Embedded-IPv4 offsets are correct (verified by program).**
>
> | Form | Bytes read | Examples checked |
> |---|---|---|
> | NAT64 `64:ff9b::/96` | `b[12..15]` | `64:ff9b::a9fe:a9fe` → 169.254.169.254, blocked; `64:ff9b::a00:5` → 10.0.0.5, blocked; `64:ff9b::808:808` → 8.8.8.8, passes |
> | IPv4-compatible `::/96` | `b[12..15]` | `::127.0.0.1` and `::a9fe:a9fe` blocked; `::808:808` passes |
> | 6to4 `2002::/16` | `b[2..5]`, the AABB:CCDD in `2002:AABB:CCDD::/48` | `2002:a00:5:1234::1` → 10.0.0.5, blocked; `2002:808:808:ffff:a00:5::` → 8.8.8.8, passes |
>
> The last 6to4 case is correct behaviour: the host bits never route to 10.0.0.5, only the embedded relay address does. `2002::` embeds 0.0.0.0 and is blocked, which is harmless.
>
> **Everything else I checked is blocked:**
> - IPv4-mapped addresses, via `Unmap`.
> - The whole 100.64/10 range, including `::ffff:100.64.0.1`, plus 0/8, 192.0.0.192, 198.18/15 and 240/4 including 255.255.255.255.
> - All multicast: 224.0.0.1, 239.1.1.1, ff01::1, ff02::1, ff05::1.
> - Other IPv6: fec0::1, Teredo `2001:0:…`, local-use NAT64 `64:ff9b:1::…`, fd00:ec2::254, `64:ff9b::6464:64c8` (NAT64 of 100.100.100.200).
> - `nil` and a 5-byte slice, which fail closed.
>
> **No false positives found:**
> - **Real hosts:** every resolved IPv4 and IPv6 address passes for hooks.slack.com, slack.com, api.slack.com, hooks.slack-gov.com, github.com, api.github.com, login.microsoftonline.com, api.openai.com, api.anthropic.com, google.com, cloudflare.com, outlook.office.com, discord.com, api.pagerduty.com and example.com.
> - **Literals at range edges:** 100.63.255.255, 100.128.0.0, 198.17.255.255, 198.20.0.0, 223.255.255.255, 192.0.1.1, 192.0.3.1 and 2001:1::1 pass. So do 2001:4860::1, 2001:4860:4860::8888 and 2606:4700:4700::1111; the `2001::/32` Teredo entry does not catch other 2001: addresses.
> - **Order of checks:** `Unmap`, then the embedded IPv4 check, then `isBlockedAddr(addr)` is sound. `v4` prefixes never match `v6` addresses in `netip`, so there is no cross-family confusion.
>
> ## SendSlack redaction (`internal/alerting/dispatcher.go`)
> - `http.Client.Do` always returns a `*url.Error`. Unwrapping it to `slack webhook <Op>: <ue.Err>` drops the URL.
> - The remaining cause texts can contain only the host or IP, never the secret path:
>   - `*net.OpError` gives "dial tcp IP:443".
>   - `*net.DNSError` gives the host.
>   - x509 errors give the host.
>   - Timeouts give no address at all.
> - The `NewRequest` parse error is replaced with a fixed string.
> - `TestSendSlack_ErrorNeverContainsURL` covers the blocked, DNS-failure and unparseable cases, and passes.
> - Both log sites are now safe: `settings.go` `slog.Warn` and `engine.go:197` `log.Printf`.
>
> ## `settings.go` CloseIdleConnections
> `defer client.CloseIdleConnections()` runs after both the production and the test-override client are built, so it covers both. This is correct.
>
> ## Findings
>
> ### Low: SIIT "IPv4-translated" `::ffff:0:0/96` is not unwrapped
> - **Where:** `netguard.go`, `embeddedIPv4` and `IsBlocked`.
> - **Verified:** `::ffff:0:7f00:1` and `::ffff:0:a9fe:a9fe` both return `blocked=false`.
> - **Why:** `Unmap` only handles `::ffff:a.b.c.d`, and `::ffff:0:x` is a separate prefix.
> - **Scenario:** it is exploitable only if eami-api's network has a stateless SIIT translator (RFC 7915/6145) that routes `::ffff:0:0/96`. That is rare, and absent in the Compose deployment.
> - **Fix:** add `::ffff:0:0/96` to the `b[12..15]` case in `embeddedIPv4`, or add it to `blockedPrefixes`. Add one test row.
>
> ### Info
> - **NAT64 network-specific prefixes (RFC 6052: /32, /40, /48, /56, /64, with the u-octet) cannot be detected.** An operator-chosen NSP is invisible to the guard. It is operator-configured, not tenant-controlled. Consider documenting that deployments with an NSP must add it to `blockedPrefixes`.
> - **ISATAP addresses (`…::5efe:a.b.c.d` / `…::200:5efe:…`) are not unwrapped.** For example, `2600::5efe:a9fe:a9fe` passes. They matter only on an ISATAP link and are not realistic in a cloud VPC.
> - **Other reserved ranges are not listed.** `192.88.99.0/24` (deprecated 6to4 relay anycast), `100::/64` (discard) and `2001:20::/28` (ORCHIDv2) pass. None of them routes to an internal target. Optional hardening only.
> - **`192.0.0.0/24` blocks two anycast addresses.** It includes 192.0.0.9 and 192.0.0.10, the PCP and TURN anycast addresses that IANA marks as globally reachable. No webhook or tool target is expected there, so there is no practical false positive.
> - **Deferred follow-ups:** the coordinator recorded five items as deferred, not fixed in B-238, and I agree they are outside this delta. They are the internal-DNS timing oracle, sequential dispatch letting one tenant stall others, the send-time https re-check, a hooks.slack.com allowlist, and SMTP via netguard when it is built.
>
> ## Files reviewed
> - `C:\AI\EAIM\eaim\eami-api\internal\netguard\netguard.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\netguard\netguard_test.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\alerting\dispatcher.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\alerting\webhook_test.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\settings.go`

## 6. Recorded, not fixed here (proposed follow-ups; no B-IDs minted)
- **Timing on internal DNS names** (security Low): save and send are byte-identical, but a resolvable-and-blocked internal name answers slightly faster than NXDOMAIN. This reveals only which internal names exist, not ports or content. The residual can be removed by allowlisting the webhook host (`hooks.slack.com`), which is a product decision.
- **One tenant can stall alert delivery** (security Low): the engine evaluates and dispatches rules sequentially. A public blackhole webhook costs up to the 10 s timeout per firing rule; before the fix there was no bound at all. The fix is asynchronous or bounded-concurrency dispatch.
- **Legacy `http://` public webhook rows** (Info): send time blocks internal targets but doesn't re-require https. This deployment has **0** `notification_config` rows, so there is nothing to migrate here.
- **SMTP** (Info): `email_smtp_host` and `email_smtp_port` are stored unvalidated but never used to send. When email sending is built, it must dial through `netguard.DialContext` and validate on save.
- **UI/contract drift** (code review Info, pre-existing): `openapi.yaml` and `SettingsPage.tsx` expect `{success, error}` from the test route, but the handler returns `{sent, reason}`, so the UI shows the result as an error toast either way. The contract is owned by Architect-EAMI.
