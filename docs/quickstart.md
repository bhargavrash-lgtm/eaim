# EAMI Quickstart — Zero to First AI Call in 15 Minutes

## Prerequisites

- Docker 24+ and Docker Compose v2 (`docker compose version`)
- Linux or macOS (Windows: use WSL2)
- Ports available: 5173 (UI), 8081 (API), 8080 (gateway), 8888 (collector)
- Git

---

## Step 1 — Install the EAMI server (3 min)

```bash
git clone https://github.com/your-org/eami.git
cd eami
./scripts/setup.sh
```

`setup.sh` will:
- Generate random service keys and write them to `.env`
- Hash an admin password and write it to `.env`
- Start the full stack with `docker compose up -d`

At the end you will see:

```
✅ EAMI is running
   UI:        http://localhost:5173
   Admin:     admin@example.com
   Password:  <shown once — save it>
```

**Troubleshooting:** If any container exits, check `docker compose logs <service>`. The most common cause is a port conflict — edit `.env` to change ports.

---

## Step 2 — Install the endpoint agent (3 min)

Download the installer for your target machine from the [GitHub Releases](https://github.com/your-org/eami/releases/latest) page.

### Windows (MSI)

```powershell
msiexec /i eami-agent.msi `
  COLLECTOR_URL=http://<server-ip>:8888 `
  API_KEY=<COLLECTOR_API_KEY from .env> `
  /quiet
```

### macOS (.pkg)

```bash
sudo installer -pkg eami-agent.pkg -target /
sudo tee /etc/eami-agent.yaml <<EOF
agent:
  interval_secs: 60
collector:
  url: http://<server-ip>:8888
  api_key: <COLLECTOR_API_KEY from .env>
EOF
sudo launchctl load /Library/LaunchDaemons/com.eami.agent.plist
```

### Linux (.deb)

```bash
sudo EAMI_COLLECTOR_URL=http://<server-ip>:8888 \
     EAMI_API_KEY=<COLLECTOR_API_KEY from .env> \
     dpkg -i eami-agent.deb
sudo systemctl enable --now eami-agent
```

### Linux (.rpm)

```bash
sudo EAMI_COLLECTOR_URL=http://<server-ip>:8888 \
     EAMI_API_KEY=<COLLECTOR_API_KEY from .env> \
     rpm -i eami-agent.rpm
sudo systemctl enable --now eami-agent
```

Verify the agent is running:

```bash
# Linux / macOS
systemctl status eami-agent   # Linux
launchctl list | grep eami    # macOS

# Windows (PowerShell)
Get-Service eami-agent
```

---

## Step 3 — See your first discovery (2 min)

1. Open `http://localhost:5173` in your browser
2. Log in with the admin credentials from Step 1
3. Go to **Discover** in the left nav
4. Wait up to 5 minutes — your machine's hostname should appear

Click the hostname to see detected AI apps, local models, MCP servers, and browser extensions.

**Not appearing?** Check the collector logs:
```bash
docker compose logs eami-collector --tail 50
```
Common causes: wrong `api_key`, firewall blocking port 8888, or the agent not yet started.

---

## Step 4 — Connect Claude Desktop through the gateway (3 min)

### Register a gateway agent

1. In the UI, go to **Gateway → Agents → Register Agent**
2. Fill in: Name, Model (`claude-sonnet-4-6`), Owner, Scope
3. Click **Register** — copy the agent token shown once

### Configure Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or
`%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "eami-gateway": {
      "url": "http://<server-ip>:8080/v1/mcp/sse",
      "headers": {
        "Authorization": "Bearer <agent-token>"
      }
    }
  }
}
```

Restart Claude Desktop. Make any tool call (e.g. ask Claude to list files). You should see the call appear in **Gateway → Audit Log** within seconds.

---

## Step 5 — Create your first policy (2 min)

1. Go to **Gateway → Policies → New Policy**
2. Set:
   - **Name:** Block bash
   - **Condition:** `tool = bash`
   - **Action:** Deny
3. Click **Save**

Test it: ask Claude Desktop to run a bash command. The request is blocked, and the denial appears in the Audit Log with your policy ID.

---

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Gateway not starting | `docker compose logs eami-gateway` — look for DB connection errors |
| Agent not appearing in Discover | Collector logs: `docker compose logs eami-collector` |
| Can't log in | Re-run `./scripts/setup.sh` to regenerate admin credentials |
| Claude Desktop not connecting | Verify the agent token is correct; check `docker compose logs eami-gateway` |
| Policy not blocking | Confirm the policy status is **Active** and priority is correct |

---

## What's next

- **FinOps:** Go to **FinOps** to see real-time token spend per agent and model
- **Alerts:** Go to **Settings → Alert Rules** to set spend thresholds with Slack notifications
- **Approvals:** Set a policy action to **Escalate** — approvals appear in **Ops → Approvals** with Slack notifications
- **More agents:** Repeat Step 2 on other machines — all appear in Discover automatically
