# Discovery administration: three-layer model and deep-discovery tiers (investigation)

**Date:** 2026-09-30 · **By:** Claude Code · **Type:** investigation only; no code, no schema, no B-IDs minted.
**Read first:** CLAUDE.md, CONTEXT.md, DESIGN_SYSTEM.md §7.7, the roadmap's Horizon 1 (B-139), BACKLOG B-054/B-139/B-164/B-165/B-170, and the brief.

## ⚠ Four things to flag before the plan

1. **"B-054" isn't the MDM-packaging item.** In BACKLOG.md, B-054 is "`setup.sh`'s `write_env` is missing 3 env vars" (DONE 2026-08-11), and the roadmap has **no B-054 entry**.
   - The real installer work is **`tasks/TASK-054`** (installer smoke tests), plus the open Tier-B follow-up at BACKLOG.md:116 (`.pkg` on real macOS hardware).
   - I traced the installers themselves (§1.1). Please confirm that's what was meant.
2. **The brief's "agentless component" is not what B-139 describes.**
   - B-139 (Horizon 1) is framed as **passive, network-level** detection for devices where no agent can be installed: mirror-port or proxy traffic inspection, or DNS-query inspection (B-164 Part C).
   - This brief asks for **active, credentialed** discovery of Windows, Linux, VMs and hypervisors.
   - These are different mechanisms, with different deployment, legal and credential-risk profiles. The plan below treats the active probe as **its own item**, not "B-139 built". Decision **D1**.
3. **"Configure" is not a tab.** It's a row in Agent Detail's **Actions** tab (`AgentActionsTab.tsx`) and a button on the Agents list. It opens `AgentConfigPanel`, which edits `agent_configs`. The substance of the brief holds: it's a Layer-3-style editor standing in for a Layer 1 that doesn't exist.
4. **Roadmap mapping.**
   - The agentless probe maps to Horizon 1 (B-139, once D1 is decided).
   - Layer 1 (presets and packaging) and the deep-discovery tiers have **no roadmap item today**. They sit nearest Horizon 0's Discovery module ("endpoint agent, browser paste-detection, identity link") and Horizon 1's CMDB completion.
   - Per the roadmap rule, they need a roadmap line before any build brief (**D2**).

## PART 1 — The three-layer admin model

### 1.1 What's real today (traced)

**Layer 1 (fleet-level presets and packaging): does not exist.**
- **Installers are static, CI-built artifacts,** one per platform:
  - Windows: `.msi` (`installer/Product.wxs`, `build.ps1`)
  - macOS: `.pkg` (`installer/macos/build.sh`, `postinstall`, `io.eami.agent.plist`)
  - Linux: `.deb` and `.rpm` (`installer/linux/nfpm.yaml`, `postinstall.sh`)
- **Only connection settings are injected at install time.** These are the collector URL, the collector API key and an optional CA-cert path:
  - Windows: MSI properties (`COLLECTOR_URL`, `COLLECTOR_API_KEY`, `COLLECTOR_CA_CERT_PATH`) written to `HKLM\SOFTWARE\EAMI\Agent`.
  - macOS: Jamf-style script parameters `$4`/`$5`/`$6`, or `EAMI_COLLECTOR_*` environment variables.
  - Linux: environment variables at `dpkg`/`rpm` time.
- **Detection settings are not injectable at install time.** They are the scan interval, enabled scanners, model paths and minimum model size. They come from the local `eami-agent.yaml` defaults (or a hand-edited `/etc/eami/agent.yaml`).
- **There is no generated package, no per-org or per-group bundle, and no preset concept** anywhere before installation.

**Layer 2 (push to deployed endpoints): real, but keyed to the wrong identity.**
- **Table:** `agent_configs` (13 rows) holds `agent_id → gateway_agents`, `scan_interval_seconds`, `model_scan_paths[]`, `max_report_size_bytes` and `enabled_scanners[]`.
- **The path:**
  1. The agent polls `GET {collector}/v1/agent-config/{discovery agent_id}` (`eami-agent/internal/collector/sender.go` `FetchConfig`).
  2. The collector proxies it to `eami-api GET /v1/agents/{agent_id}/config` with a service key.
  3. The API resolves the **endpoint → its linked governed agent** (`endpoints.gateway_agent_id`) and returns **that governed agent's** `agent_configs` row (`agent_config_remote.go`).
  4. The agent applies any non-zero fields at runtime, with no restart.
- **Consequences:**
  - **An endpoint gets remote config only if an admin has manually linked it to a governed agent.** Unlinked endpoints get 404 and silently keep their local YAML.
  - **Scanner configuration (an endpoint concern) hangs off an AI-agent identity (a Gateway concern).** One governed agent's config silently becomes the scanner config for every endpoint linked to it.
  - **It's single-org:** the API resolves the org with `GetDefaultOrgID` (the B-236 context).
  - Edits go through `PUT /v1/gateway/agents/{id}/config` (admin and operator) via `AgentConfigPanel`.

**Layer 3 (per-endpoint view): today it's the only editor.**
- The only UI that edits scanner config is the Configure panel described above.
- It's reached from an *agent*, not from an endpoint. Discover's `EndpointDrawer` shows the endpoint's report but not its effective config.

**`eami-collector`: what it is and isn't (so the new component can't be confused with it).**
- **What it is:**
  - an on-prem **passive HTTP receiver and relay**;
  - `POST /v1/ingest` takes agent reports (per-agent API keys in its own SQLite), with a SQLite write-ahead buffer, forwarding to `eami-api`;
  - `GET /v1/agent-config/{id}` is a **config proxy** to the API;
  - `mint-key`, `revoke-key` and `list-keys` make it the **key-management CLI** too.
- **What it isn't:** it **never originates traffic toward endpoints**. It doesn't scan, probe, hold target credentials or schedule anything. It only answers agents that call it.
- **The new agentless component must therefore not be called "Collector"**, and it must not be folded into it (§1.2b).

**B-139: confirmed zero-built.** Its entry says "logged, investigation not started". B-164 Part C re-confirmed that no network, DNS or proxy detection code exists; the dormant `discovered_endpoints`/`POST /v1/reports` surface is unrelated. A repo-wide search finds no agentless code.

### 1.2 The new model

**a) Layer 1: reusable config presets (agent-based).**
- **Concept:** a **Discovery Preset** is a named, versioned scanner configuration that endpoints are *assigned to*. It replaces keying config by governed agent.
- **Proposed schema (for a later brief to confirm):**
  - `discovery_presets`:
    - identity: `id`, `org_id`, `name`, `description`;
    - fields: `scan_interval_seconds`, `enabled_scanners text[]`, `model_scan_paths text[]`, `max_report_size_bytes`, `model_file_size_mb`, `is_default bool` (exactly one per org);
    - bookkeeping: `version int` (bumped on every change), `created_by`, `created_at`, `updated_at`.
  - The scanner fields are exactly what `agent_configs` and `eami-agent.yaml` carry today, plus the one YAML-only field.
  - `endpoints.discovery_preset_id` (nullable FK; NULL means the org's default preset). **Assignment is per endpoint, never per governed agent.**
  - **Enrollment keys** (`discovery_enrollment_keys`: `id`, `org_id`, `preset_id`, `key_hash`, `prefix`, `expires_at`, `revoked_at`) replace the shared per-agent collector key typed into an MDM field. The key identifies org and preset at first ingest. That also fixes the single-org `GetDefaultOrgID` resolution.
- **How a preset binds to a "generated package":**
  - **Don't rebuild binaries per preset.** That would break code signing and notarization (MSI Authenticode, the `.pkg` Developer ID and notarization) and multiply release artifacts.
  - Instead, a **deployment bundle** is:
    - the existing signed installer, unchanged;
    - plus preset-specific **install parameters**: the collector URL, the preset's enrollment key and the CA path;
    - rendered for each channel: an MSI command line or `.mst` transform for Intune/SCCM, Jamf script parameters `$4`–`$7`, and env-var lines for Ansible/`dpkg`.
  - The agent then pulls the preset's full scanner config at runtime (Layer 2). The installer carries only identity and connectivity, never the scanner config, so a preset edit reaches deployed endpoints without repackaging.
- **Migration:** each of today's 13 `agent_configs` rows becomes a preset. Endpoints linked to those governed agents are assigned to the matching preset. The remote-config endpoint then resolves **endpoint → preset**.

**b) The agentless component: "Discovery Probe"** (binary `eami-probe`). It is *not* "Collector".
- **Naming:** "Probe" is exact. It actively probes targets on the admin's authority, where the collector passively receives. "Scanner" would clash with the agent's detection scanners (`enabled_scanners`).
- **Shape:**
  - An on-prem service, packaged like the collector: a container on the appliance, or standalone.
  - It registers once with the API using a one-time **probe registration token**, and generates its own keypair (§ credentials below).
  - It **pulls** jobs: target ranges, credential references, schedule and allowed protocols. It never accepts inbound commands.
  - It writes results through the existing ingest path, tagged `source=probe`.
- **Results model:** agentless finds land as the **same endpoint asset kind**, with a `discovery_source` of `agent`, `probe` or `both`.
  - **Identity matching with agent-reported endpoints is genuinely hard.** Hostname, IP and MAC all drift, and DHCP reuses addresses. This is a real part of the first brief, not a detail.
- **Realistic v1 coverage:**

| Target | v1 mechanism | What it yields | Out of v1 |
|---|---|---|---|
| Linux, and macOS by the same path | **SSH**, key-based preferred, **read-only command allowlist** | the same signals the agent collects (processes, listening ports, model dirs, `nvidia-smi`, pip/npm AI packages, docker) | agent-parity edge cases, sudo-only data |
| Windows | **WinRM over HTTPS** (Kerberos preferred; NTLM only by explicit opt-in), read-only PowerShell | processes, services, installed AI apps, GPUs, listening ports | WMI/DCOM, SMB-based methods |
| Hypervisors | **vCenter/vSphere REST** (read-only role); **Proxmox API**; Hyper-V via WinRM | an **inventory** of VMs, guest OS and GPU passthrough/vGPU (**not** in-guest processes) | cloud provider APIs, Nutanix, XenServer |
| Unmanaged hosts in range | **TCP connect probe** on known AI-serving ports (e.g. 11434 Ollama, 8000 vLLM, 8080 llama.cpp/TGI, 1234 LM Studio, 8000–8002 Triton) | "an AI serving endpoint answers here", which is the Tier-2 trigger | OS fingerprinting, SNMP, passive traffic (**B-139's** original mechanism) |

- **Target ranges:**
  - They are an explicit, admin-entered **allow list** (CIDRs or hosts) plus exclusions, with a per-range size cap and a rate limit.
  - The probe **only ever touches addresses on the list.** Scanning is an act the org must authorise, so the range list *is* the authorisation record, and every range change is audited.
- **Credentials, a real secrets-management surface.** It's stricter than tool credentials, because these are domain, root or vCenter-level secrets:
  - **Encryption at rest:** AES-256-GCM at minimum, as `toolcreds` does. But `toolcreds` has one static key from env, **no key version** and the **API can decrypt**.
  - **Sealed to the probe:** the API should **never be able to decrypt** probe credentials. The admin UI encrypts each secret to the **probe's public key**; the API stores ciphertext it cannot read; only the on-prem probe's private key opens it. Hybrid encryption (X25519 key agreement → AES-256-GCM) keeps AES-256-GCM as the data cipher. A compromised SaaS database or API then yields no usable target credentials.
  - **Binding:** AAD binds each ciphertext to `(org_id, credential_id, probe_id)`, so a blob can't be swapped onto another row.
  - **Key versioning and rotation:** a probe re-keys and re-seals on rotation.
  - **Write-only:** never returned by any API, with the same "leave blank to keep" semantics as tool credentials.
  - **Access:** **admin-only**, plus **step-up (B-231)** to create or change one. Every create, update, use and failure is **audited**.
  - **Least privilege:** published guidance names the read-only role per protocol (a vCenter read-only role, Linux via a restricted account and a command allowlist, Windows via JEA-constrained endpoints).
- **Onboarding hook:** B-139's own note already asks for a setup-wizard integration point for the agentless component. It carries over to the probe.

**c) Where Layer 1 lives in navigation: its own destination, not a Settings tab.**
- **Why:** it has real sub-objects with their own lifecycles: presets, enrollment keys and bundles, probes, target ranges, credentials and schedules. Settings is org-wide single-page configuration, and cramming this in would repeat the §8 anti-pattern.
- **Where:** a **"Discovery setup"** destination in C11's sidebar regroup (the configure/admin group), next to where Discover retires into Assets (C4).
- **One-spine check:**
  - Endpoints stay the identity, visible in Assets and Endpoint Detail.
  - Changes are audited in the existing audit trail.
  - **No separate identity model and no separate audit trail.**

**d) Layer 3's corrected scope: a read-only view, once Layer 1 exists.**
- On **Endpoint Detail (C2)**, show the endpoint's **effective config**: its assigned preset (name and version), the values in force, and the **last config actually applied** by the agent (this needs the agent to report its applied preset version: one field, confirm in the brief).
- The only write is a **narrow "change preset assignment"** control, admin-only. **No per-endpoint field editing.** Presets are the edit surface.
- The Agent Detail Configure action is **retired**. Scanner config was never a property of an AI agent.

## PART 2 — Deep-discovery tiers (triggered, not universal)

### 2.1 LLM deep discovery: characterising a customer-hosted model

**What's already real:** Tier 1 already makes one live call. The agent's `models` scanner fetches a local **Ollama `GET /api/tags`**. `mcp_servers` does a live TCP port probe. Tier 2 extends this; it isn't a new kind of act.

**Realistic characterisation APIs**, surveyed. Every call is metadata or health only (no inference):

| Server | Identity and version | Model metadata | Health and responsiveness |
|---|---|---|---|
| **Ollama** | `GET /api/version` | `GET /api/tags` (name, size, digest, family, parameter size, quantization); `POST /api/show` (architecture, context length, license, template, parameters); `GET /api/ps` (loaded models, VRAM) | latency of the above |
| **OpenAI-compatible** (vLLM, llama.cpp server, LM Studio server, LocalAI, SGLang) | vLLM `GET /version`; llama.cpp `GET /props` | `GET /v1/models` (ids; vLLM adds `max_model_len` and `root`) | vLLM and llama.cpp `GET /health`; vLLM `GET /metrics` (Prometheus) |
| **HF TGI** | `GET /info` (`version`, `model_id`, `model_sha`, `dtype`, token limits) | same | `GET /health`, `GET /metrics` |
| **NVIDIA Triton** (KServe v2) and **NIM** | `GET /v2` (server metadata); NIM `GET /v1/metadata` | `GET /v2/models/{m}`, `/config`; NIM `GET /v1/models` | `GET /v2/health/ready`; NIM `GET /v1/health/ready` |

- **Fingerprinting:** the probe identifies the server type from which endpoints answer and their response shapes (and `Server` headers), in a fixed order. It **never brute-forces credentials.**
- **Unauthenticated endpoints:** many internal model servers accept anonymous requests. **That is itself a security finding** worth surfacing ("model server accepts unauthenticated requests").
- **Authenticated endpoints:** characterisation stops at what is anonymously readable, unless an admin supplies a credential. That credential is stored the Probe way (§1.2b).
- **No inference calls by default:** a generation call costs compute, may be logged by the server, and implies prompt content. An optional, admin-enabled, one-token canary for "does it actually serve" is a later decision, not v1.
- **Who probes what:**
  - Models on an agent's own host: the **endpoint agent over localhost**, extending its existing Ollama call.
  - Network-hosted model servers found by the probe's port sweep or reported by agents: the **Discovery Probe**, only inside allow-listed ranges.
- **An honest correction to "feeds directly into Model Details":** §7.7's **Model Details** tab sits on **Tool Detail**, which is a `gateway_tools` row of type `ai_provider`.
  - A *discovered* model server is **not a tool** until someone registers it.
  - Characterisation data needs a home before registration: the discovered endpoint or asset, shown on **Endpoint Detail (C2)** as a "Model server" section.
  - When an admin registers it as an `ai_provider` tool, **Model Details reads the same characterisation record.**
  - So it becomes the data source for Model Details only after registration. Whether a model server is its own CMDB asset kind is decision **D5**.

### 2.2 Agent deep discovery: two cases, never conflated

**Case 2a: unsanctioned or shadow agents (never dispatching through rheoARC).** The depth is observational only. **rheoARC cannot see inside a call it never routed**, so the UI must never imply dispatch-level insight.

**Real, non-invasive signals available today** (from the agent's report JSON, traced to the scanner structs):

| Signal | Source | Fields actually collected | Shown in the UI today? |
|---|---|---|---|
| Running AI processes | `ai_processes` | `pid`, `name`, `exe_path`, `command_line`, `detected_at` (scan time) | **No.** It's collected and stored, but the drawer shows 8 sections and this isn't one. |
| Direct provider connections | `network_activity` | `remote_host` (a **fixed list**: OpenAI, Anthropic, Google, Cohere, Mistral, Azure inference, Bedrock), `remote_port`, `process_name`, `pid`, `state`; DNS-cache hits | Yes (Network Activity) |
| Agent frameworks and SDKs | `python_envs`, `nodejs_ai` | AI packages per environment or project | Yes |
| Tool wiring | `mcp_servers` | name, command, args, source (Claude Desktop, Cursor, VS Code), port, live flag | Yes |
| Credentials present | `cloud_clients` | presence of provider env vars or credential files (**never values**) | Yes |
| Installed AI apps | `ai_apps` | name, version, path, source | Yes |
| Local models | `models` | files and Ollama models, `modified_at` (file mtime) | Yes |

**The strongest real signal already exists: a PID that is an AI-framework process *and* holds a live connection straight to a provider host.** In other words, an agent that bypasses the gateway. That's derivable today, by joining `ai_processes` to `network_activity` on PID within one report.

**Not real today, so not to be implied:**
- **install timestamps** (`ai_apps` has none; `detected_at` is scan time);
- process **start time**;
- the owning **user**;
- **call volume or content**;
- provider hosts outside the fixed list;
- **persistence**: `scheduled_tasks` and `file_changes` exist in code but are **not wired** into the report.

"Install timestamp" in the brief would need new instrumentation per platform. That's a scoping decision, not an existing signal.

**"Onboard as Governed Agent": there is no clean path today, so this needs new work.**
- **Today's steps are all manual and separate:**
  1. An admin creates a governed agent.
  2. An admin mints its API key.
  3. **The agent's owner changes that agent's own code or config** to call the gateway (MCP SSE with a gateway token).
  4. Optionally, an admin links the endpoint.
- **Two structural limits:**
  - rheoARC **cannot redirect** an existing process's traffic, so onboarding is always a *request to the owner*, not a switch.
  - The endpoint link is **one governed agent per endpoint** (`endpoints.gateway_agent_id`), and **no process-level identity** exists. Several shadow agents on one host can't each be tied to their own governed agent.
- **Proposed action (a later brief):** on a shadow-agent finding, "Onboard as Governed Agent" opens a guided flow:
  1. create the governed agent, prefilled from the process name and endpoint;
  2. mint its key (admin);
  3. show the integration snippet for the detected framework or provider;
  4. record a **process-level finding → governed agent** mapping (new).
- **Honest completion:** the finding shows "onboarding in progress" until the governed agent's **first real dispatch** (Lineage's `first_seen`). It is never marked "governed" just because a record was created.
- **Tie-in:** this overlaps B-170 (setup-wizard governed-agent onboarding), so the two should share the create-agent flow.

**Case 2b: Governed Agents (already dispatching through the gateway).** **No new mechanism.** **Lineage** (shipped 2026-09-29) *is* the deep view, fed continuously by real dispatch data, not by a probe. The only link needed is from a Discovery finding for a governed agent to its Agent Detail → Lineage, via the existing `AgentLink` pattern.

## Founder decisions (2026-09-30)

- **D1:** the probe is a **new item, B-267**, separate from B-139. B-139 stays passive network inspection.
- **D2:** roadmap lines were added for presets, B-267 and the deep-discovery tiers.
- **D3:** "B-054" meant **TASK-054** (the founder's error in the brief).
- **D4:** probe credentials are **locked to the probe process**. The API holds them but can't decrypt them. They are write-only, rotatable, admin-only behind step-up (B-231) and fully audited, a stricter standard than tool credentials.
- **D5:** a discovered but unregistered model server gets a real section on Endpoint Detail before Model Details applies.
- **D6:** ship on the real signals first. Install and start-time capture is **B-268** (future, not blocking).

**Approved build order (final, 2026-09-30):**
1. **B-269** presets.
2. **B-270**, the read-only Endpoint Detail view, alongside C2.
3. Shadow-agent surfacing and onboarding.
4. **LLM characterisation on the endpoint's own machine.** It extends the agent's Ollama call, with the same discovery pass and trust boundary, and needs no new credential or scan-range work.
5. **B-267**, the probe, which also takes on network-hosted model characterisation.

## Decisions that were needed before any build brief (as originally asked)

| # | Decision | Recommendation |
|---|---|---|
| D1 | Is the active, credentialed Discovery Probe **B-139**, or a new item (with B-139 staying passive network inspection)? | A **new item.** B-139 keeps its passive-inspection framing as a later, separate mechanism. |
| D2 | Roadmap placement for Layer 1 (presets and packaging) and the deep-discovery tiers | Add them as Horizon 1 lines under Discovery or CMDB completion, **before** briefing |
| D3 | Confirm "B-054" meant **TASK-054** (installers) | — |
| D4 | Credential model: probe-sealed (the API can't decrypt), or `toolcreds`-style (the API decrypts)? | **Probe-sealed**, given domain/root-level secrets |
| D5 | Is a discovered model server its own CMDB asset kind, or a section on the endpoint? | An endpoint section first. Promote to its own kind only if real volume needs it |
| D6 | Is process start time or install time worth new agent instrumentation? | Defer. Ship 2a on the real signals above first, and surface `ai_processes` in the UI |

**Suggested sequencing, once D1–D2 are decided:**
1. **Presets and enrollment:** Layer 1 and the Layer 2 re-key. This also fixes endpoint config depending on a governed-agent link, and the single-org resolution.
2. **Layer 3 read-only** on Endpoint Detail, with C2.
3. **2a surfacing:** show `ai_processes`, plus the PID-joined "bypasses the gateway" signal, plus the onboarding flow.
4. **LLM characterisation over localhost**, extending the agent's existing Ollama call.
5. **The Discovery Probe**, with its credential vault, range authorisation and network characterisation. This is the largest step and gets its own investigation-to-build sequence.
