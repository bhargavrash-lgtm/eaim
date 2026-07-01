# Task: Write customer-facing quickstart guide
**From:** PM-EAMI  
**To:** PM-EAMI  
**Priority:** normal  
**Blocked by:** TASK-052 (setup.sh must be working on macOS)

## What I need

A `docs/quickstart.md` that takes a customer from zero to their first AI call through EAMI in 15 minutes. Written for a technical administrator (understands Docker and CLI, not necessarily a Go developer).

## Structure

### Prerequisites (2 min)
- Docker 24+ and Docker Compose v2
- Linux or macOS (Windows via WSL2)
- Port 5173 (UI), 8081 (API), 8080 (gateway), 8888 (collector) available

### Step 1 — Install EAMI server (3 min)
```bash
git clone https://github.com/your-org/eami.git
cd eami
./scripts/setup.sh
```
Expected output: admin credentials, stack URL.

### Step 2 — Install the endpoint agent (3 min)
Three sub-paths: Windows (MSI), macOS (.pkg), Linux (.deb/.rpm).
Each: download link from GitHub Releases, install command, verify `systemctl status eami-agent` or equivalent.

### Step 3 — See your first discovery (2 min)
- Open UI at http://localhost:5173
- Log in with admin credentials from Step 1
- Navigate to Discover — endpoint should appear within 5 minutes

### Step 4 — Connect your AI client through the gateway (3 min)
- In the UI, go to Gateway → Agents → Register Agent
- Copy the gateway SSE URL: `http://your-server:8080/v1/mcp/sse`
- Configure Claude Desktop: edit `claude_desktop_config.json` to point to the gateway URL
- Make a tool call in Claude Desktop — see it in the Audit log

### Step 5 — Create your first policy (2 min)
- Go to Gateway → Policies → New Policy
- Action: `deny`, condition: tool = `bash`
- Test: ask Claude to run a bash command — it should be blocked
- See the denied entry in the Audit log

### Troubleshooting section
- Gateway not starting: check `docker compose logs eami-gateway`
- Agent not appearing: check collector logs, verify API key matches
- Can't log in: re-run `./scripts/setup.sh` or check admin credentials in `.env`

## Acceptance criteria

- [ ] `docs/quickstart.md` exists
- [ ] A complete technical newcomer can follow it without additional help
- [ ] All commands are copy-pasteable and correct
- [ ] Covers Windows, macOS, and Linux agent install
- [ ] Under 500 lines — concise over comprehensive

## Files to create

- `docs/quickstart.md`
- `docs/` directory (create if missing)
