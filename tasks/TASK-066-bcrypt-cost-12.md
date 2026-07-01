# Task: Bump bcrypt rounds from 10 to 12 in setup.sh
**From:** PM-EAMI
**To:** DevOps-EAMI
**Priority:** normal
**Blocked by:** none

## What I need

Change bcrypt cost from 10 to 12 in `scripts/setup.sh`. One line change:

```bash
# Line 233 — change:
local pycode="import bcrypt, sys; pw = sys.stdin.buffer.read(); print(bcrypt.hashpw(pw, bcrypt.gensalt(rounds=10)).decode())"

# To:
local pycode="import bcrypt, sys; pw = sys.stdin.buffer.read(); print(bcrypt.hashpw(pw, bcrypt.gensalt(rounds=12)).decode())"
```

Also update the comment on line 230 from "10 rounds" to "12 rounds".

## Context

FINDING-006 from TASK-051-security-findings.md (MEDIUM): OWASP 2023 recommends bcrypt
cost ≥ 12 on 2026 hardware. This was supposed to be in TASK-052 scope but was omitted
from the acceptance criteria — filing as a standalone fix before v1.0 tag.

## Acceptance criteria

- [ ] `scripts/setup.sh` line with `gensalt` uses `rounds=12`
- [ ] Comment above it reads "12 rounds"
- [ ] `shellcheck scripts/setup.sh` still exits 0

## Files to modify

- `scripts/setup.sh`
