# Task: Implement browser AI extension scanner
**From:** PM-EAMI  
**To:** BE-Collector  
**Priority:** normal  
**Blocked by:** TASK-035 (collector ingest must work first)

## What I need

`eami-agent/internal/detection/browser/scanner.go` is a STUB. Implement it to detect AI-related browser extensions in Chrome and Edge.

### What to detect

Known AI browser extension IDs to match against:

| Extension | Chrome/Edge ID |
|---|---|
| Claude (Anthropic) | `ppmdhllojcbgkbmcfkhdkklblickdbno` |
| ChatGPT (OpenAI) | `bbnkhnaphfggfomhfnnbhiplfflgolge` |
| Perplexity AI | `hlgbmkakpnlgpfkipbiiknlibooaoeag` |
| Merlin AI | `camppjleccjaphfdbohjdohecfnoikec` |
| Compose AI | `ddlbpiadoechcolndfekhkjhkjebnakj` |
| Monica AI | `ofpnmcalabcbjgholdjcjblkibolbppb` |
| Grammarly | `kbfnbcaeplbcioakkpcpgfkobkghlhen` |

Add more as needed — the list should be easy to extend.

### Windows implementation (`scanner_windows.go`)

Chrome and Edge store extensions under each user's profile:
```
%LOCALAPPDATA%\Google\Chrome\User Data\Default\Extensions\{id}\
%LOCALAPPDATA%\Microsoft\Edge\User Data\Default\Extensions\{id}\
```

For each user profile found in `C:\Users\*`:
1. Check if either extension directory exists.
2. If it does, read `manifest.json` inside it to get `name` and `version`.
3. Return a `BrowserExtension` struct per match.

```go
type BrowserExtension struct {
    Browser   string // "chrome" | "edge"
    ExtID     string
    Name      string
    Version   string
    UserPath  string // anonymised: just the username, not the full path
}
```

### macOS implementation (`scanner_darwin.go`)

```
~/Library/Application Support/Google/Chrome/Default/Extensions/{id}/
~/Library/Application Support/Microsoft Edge/Default/Extensions/{id}/
```

Same logic as Windows.

### Linux implementation (`scanner_linux.go`)

```
~/.config/google-chrome/Default/Extensions/{id}/
~/.config/microsoft-edge/Default/Extensions/{id}/
```

### Cross-platform stub for unsupported platforms (`scanner_other.go`)

Return empty slice, nil error.

### Integration into payload

Add `BrowserExtensions []BrowserExtension` to the `Report` struct in `eami-agent/internal/payload/builder.go` (or wherever the Report type is defined). Call the browser scanner in `builder.go` alongside the other scanners.

### Test (`scanner_test.go`)

Write a test that creates a fake extension directory tree in a temp dir, injects it via an option, and verifies the scanner finds the correct extensions. Follow the same pattern as `models/scanner_test.go`.

## Acceptance criteria

- [ ] On Windows, running the scanner finds Chrome/Edge AI extensions if installed
- [ ] On macOS and Linux, same
- [ ] Manifest version is populated when `manifest.json` is readable
- [ ] Extension not in the known-ID list is not reported (not a catch-all)
- [ ] Missing Chrome/Edge installation does not error — returns empty slice
- [ ] Unit test with temp dir passes
- [ ] `BrowserExtensions` field appears in the agent report payload sent to collector
- [ ] `go vet ./...` exits 0 (all platforms via build tags)

## Files to create or modify

- `eami-agent/internal/detection/browser/scanner.go` — shared types + known IDs
- `eami-agent/internal/detection/browser/scanner_windows.go` — Windows scan
- `eami-agent/internal/detection/browser/scanner_darwin.go` — macOS scan
- `eami-agent/internal/detection/browser/scanner_linux.go` — Linux scan
- `eami-agent/internal/detection/browser/scanner_other.go` — stub for other platforms
- `eami-agent/internal/detection/browser/scanner_test.go` — unit test
- `eami-agent/internal/payload/builder.go` — add BrowserExtensions to report

## Files to read first

- `eami-agent/internal/detection/models/scanner.go` — best-practice scanner pattern
- `eami-agent/internal/detection/models/scanner_test.go` — test pattern with temp dir
- `eami-agent/internal/payload/builder.go` — Report struct and how scanners are called
