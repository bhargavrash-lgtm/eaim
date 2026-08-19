# EAMI appliance — base image + first-boot detection (B-053)

Builds a bootable Debian-minimal `.qcow2` appliance image that boots
straight into the full `docker-compose.prod.yml` stack, with data-volume
separation from the OS image and a first-boot detection marker the future
setup wizard will consume. Model A only (ADR-020) — packages
`docker-compose.prod.yml` exactly as it already exists, no changes to it.

## Build

Requires Packer ≥ 1.16, a QEMU/KVM-capable host (`kvm-ok` should report
"KVM acceleration can be used"), and internet access (downloads the Debian
12 generic cloud image, ~400MB, on first build).

```sh
cd appliance/packer
packer init eami-appliance.pkr.hcl
packer build eami-appliance.pkr.hcl
```

Output: `appliance/packer/output-eami-appliance/eami-appliance.qcow2`.

Build time: dominated by `apt-get install docker-ce` inside the build VM
(network-bound) — expect several minutes, not seconds.

## Deploy

The appliance expects **two virtio disks**:

1. **Disk 1 (`/dev/vda`)** — the built `eami-appliance.qcow2` itself. This
   is the OS + application-bundle disk. An "update" is: build a new image
   from a newer commit, replace disk 1, keep disk 2 attached unchanged.
2. **Disk 2 (`/dev/vdb`)** — a separate, empty virtio disk, any reasonable
   size (holds Postgres data + backups + collector buffer + certs — size
   to the deployment's expected data volume). `eami-data-disk.service`
   partitions and formats it automatically on first boot (GPT, single
   ext4 partition, labeled `EAMIDATA`) and mounts it at `/data`. It is
   **never** reformatted on a later boot — see that script's own
   dry-run-before-touching-anything guard.

No manual setup beyond attaching both disks and powering on. Boot order:

```
eami-data-disk.service   (Before=docker.service)
  → partitions/mounts /data if not already done
  → points Docker's data-root at /data/docker
docker.service starts, using /data/docker
eami-stack.service       (After=docker.service)
  → generates /data/eami/.env if it doesn't already exist
  → docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env up -d
  → writes /data/eami/state = "unconfigured" if it doesn't already exist
```

## Why secrets live on the data disk, not the OS disk

`/opt/eami/` (OS disk) is the **static application bundle** —
`docker-compose.prod.yml`, `schema/migrations-v2/`, the backup scripts —
baked in at build time and replaced wholesale by every OS image update.

`/data/eami/.env` (data disk) holds the **generated secrets**
(`POSTGRES_PASSWORD`, `TOOL_CREDENTIALS_ENCRYPTION_KEY`, etc.) and the
first-boot marker. If secrets lived on the OS disk instead, replacing that
disk on update would silently regenerate them on next boot — the Postgres
data files would technically survive (satisfying AC6's literal wording)
but become unusable, since a freshly-generated `POSTGRES_PASSWORD` won't
match the already-initialized database's own auth, and a fresh
`TOOL_CREDENTIALS_ENCRYPTION_KEY` can't decrypt data encrypted under the
old one. Keeping secrets on `/data` makes "survives an update" mean
"still actually works", not just "files still exist on disk".

## First-boot detection contract, and the setup wizard (implemented)

The setup wizard (`eami-ui`'s `/setup` route, `eami-api`'s `/v1/setup/*`
routes in `internal/api/bootstrap.go`) is now built. **The real
"is this appliance configured" gate is the `orgs` table itself, not the
`/data/eami/state` file** — a deliberate deviation from this brief's
original assumption ("whatever serves the wizard reads this file
directly"), found and required to be surfaced during that later brief's own
research rather than worked around silently: `docker-compose.prod.yml` is
frozen for that brief too, and today only `api_certs`/`gateway_certs` named
Docker volumes are mounted into `eami-api`/`eami-gateway` — nothing
bind-mounts `/data/eami` into any container, so no running service process
can actually read this host file. A DB-backed check (`SELECT count(*) FROM
orgs`) is also strictly more correct regardless: it's always live and
consistent, where a file can only be updated by whatever process happens to
be running when state changes, and no such process exists inside a
container without that mount.

`/data/eami/state` still exists, still starts as `unconfigured`
(`eami-stack.sh`, first successful bring-up), but is now **informational
only** — a human-readable console artifact, not consumed by any API or UI
code path. `eami-stack.sh` best-effort-reconciles it to `configured` on a
later boot once it observes `orgs` is non-empty (querying Postgres directly
via `docker compose exec postgres psql`, the same mechanism
`scripts/setup.sh`'s `seed_database` already uses) — but nothing depends on
that reconciliation actually having run; the API's own live `orgs` check is
always authoritative.

**Setup token generation, also in `eami-stack.sh`, same boot step:** if
`orgs` is empty, a fresh 256-bit (`openssl rand -hex 32`) single-use token
is generated, its SHA-256 hash stored in the new `setup_tokens` table
(`schema/migrations-v2/000003_add_setup_tokens.up.sql`, 30-minute expiry),
and the **raw token is printed to stdout** — which `eami-stack.service`'s
existing `StandardOutput=journal+console` already routes to the VM console,
reusing this appliance's one documented emergency-access trust boundary
(SSH is permanently disabled — see below) rather than inventing a new one.
Any prior unconsumed token is deleted first, so only one token is ever live
at a time. The wizard's `POST /v1/setup/bootstrap` endpoint requires this
token, validates and consumes it inside a single DB transaction with a real
row lock (`SELECT ... FOR UPDATE`) so a race between two near-simultaneous
submissions has exactly one winner — see `bootstrap.go`'s package doc
comment for the full design. Network reachability of the wizard's endpoints
is never sufficient on its own: without the console-displayed token, no
org/admin can be created, regardless of how well-formed the rest of the
request is.

## SSH / emergency access

**SSH is fully disabled by default**, permanently: `ssh.service` is
disabled before the image is captured (`appliance/scripts/provision-base.sh`),
every remaining local account's password is locked (`passwd -l`), and the
`packer` build account itself — the one Packer used during the build, with
its ephemeral SSH key and cloud-init's passwordless-sudo grant — is deleted
entirely (`userdel -f -r`) as the final provisioning step, not just
neutered. This was tightened during this brief's own mandatory code-review
pass: an earlier version only stripped the account's `authorized_keys`,
which closed the specific "SSH re-enabled later" path but still left a
standing passwordless-sudo account in the shipped image, unreachable today
but a latent target if any future access path to it ever opened. Full
account removal closes that more completely. There is no password-based or
key-based login of any kind, and no privileged account beyond `root`
(password-locked, GRUB-reachable only), on a normally booted appliance.

**Documented emergency-recovery path: the hypervisor's virtual console,
via GRUB.** GRUB itself is left unprotected (no GRUB password set) —
physical/hypervisor-console access to the bootloader is the trust
boundary, by design: interrupt GRUB at boot, edit the kernel command line
(e.g. append `init=/bin/bash`, or select `systemd.unit=rescue.target`),
get a root shell with no credential needed at all. This is the standard
break-glass model for hardened appliances (matches Device42 and similar) —
console/physical access is inherently privileged access, so gating it
behind a separate password would only add friction without adding real
security, while a locked-down network-facing SSH stays genuinely closed
by default. A console-triggered temporary-SSH-enable convenience script
was considered and deliberately **not** built this round — extra attack
surface and scope, out of proportion to this brief (explicit founder
decision, 2026-08-08).

**If the data disk fails to mount** (`eami-data-disk.service` shows
`FAILED` on the console, e.g. "not found" or "already has a
filesystem/partition signature that isn't labeled EAMIDATA"): this is a
deliberate fail-loud guard, not a bug — it refuses to auto-partition a
disk it doesn't recognize rather than risk silently wiping real data (see
that script's own `wipefs -n` dry-run check). Recovery is console-only,
same as above: interrupt GRUB, get a rescue shell, inspect the disk
manually (`lsblk`, `wipefs -n /dev/vdb`, `blkid`) before either relabeling
an intentionally-reused disk or attaching the correct empty one.

## TLS certificates (B-071, extended to eami-collector by B-072)

The UI, the API, agent connections to the gateway, **and endpoint-agent
connections to the collector (B-072)** are all TLS-terminated at the edge
by the `eami-proxy` container (Caddy) — `docker-compose.prod.yml`'s
`eami-ui`/`eami-api`/`eami-gateway`/`eami-collector` no longer publish
ports directly at all; only `eami-proxy`'s `443` (UI + API), `8443`
(gateway), and `8888` (collector) are reachable from outside the
appliance's own internal docker network (plus a plaintext `80` that only
ever issues a redirect to `443`, never serves content).

**B-071's original gap, now closed:** at the time B-071 shipped,
`eami-collector`'s port `8888` was explicitly out of that brief's scope
(its own CONTRACTS/ACCEPTANCE CRITERIA named only the UI/API/gateway
surfaces) and stayed plaintext, disclosed as a priority follow-up. B-072
closes it — same edge-proxy pattern, same CA, reused rather than a second
parallel mechanism.

**Default, out of the box: a self-signed certificate, generated
automatically.** Caddy's own built-in `tls internal` directive handles
this entirely — no script, no manual step, nothing to configure. The
generated certificate/CA persists across container restarts (a
`caddy_data` named volume), so it isn't silently regenerated (and isn't a
fresh, newly-browser-warned cert) every time the stack restarts. Every
browser will show a certificate warning the first time it connects — this
is expected for a self-signed cert on an appliance with no public
hostname, and is why the alternative below exists.

**To install a real certificate**, if the customer has one (a private CA,
a certificate for the appliance's actual DNS name, etc.):

1. Copy the certificate and private key onto the appliance's **data disk**
   (not the OS disk — same reasoning as every other secret in this
   document) as:
   ```
   /data/eami/certs/custom/fullchain.pem
   /data/eami/certs/custom/privkey.pem
   ```
2. Restart the proxy container:
   ```
   docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env restart eami-proxy
   ```
3. `eami-proxy`'s entrypoint checks for both files on every start and uses
   them instead of the self-signed default automatically — no other
   configuration change needed. Removing both files and restarting again
   reverts to the self-signed default.

`eami-stack.sh` creates `/data/eami/certs/custom/` (empty) on every boot,
so it's always ready for an admin to drop files into, and writes
`EAMI_CERT_DIR=/data/eami/certs` into the generated `.env` so
`docker-compose.prod.yml`'s bind mount resolves to the data disk correctly
regardless of the current working directory `docker compose` is invoked
from.

**Not built this round, a reasonable future extension:** a setup-wizard
UI step to upload a certificate through the browser instead of placing
files by hand. The file-drop path above is the deliberately simpler v1
default — matches this appliance's existing "console/file-based
configuration, no fancy UI" precedent (e.g. `GATEWAY_UI_BASE_URL`
correction) rather than building new `eami-api` storage/validation
machinery for something a `scp`+`restart` already solves correctly.

### TLS certificates for eami-collector — trusting the CA on endpoint agents (B-072)

Unlike the UI/API/gateway (reached by a browser, which can click through a
self-signed-cert warning once), `eami-collector` is reached by
`eami-agent` running unattended, non-interactively, on every managed
endpoint — there's no human present to click through anything, and the
whole point of an installer-driven rollout is zero-touch deployment across
many machines. So a real endpoint agent needs to be told to trust the
appliance's CA *at install time*, not discover a warning at runtime.

**If the collector is using a real, publicly-trusted certificate**
(customer-supplied, per the section above): nothing to do here — it's
already in every OS's default trust store, exactly like any other HTTPS
site.

**If the collector is using the default self-signed certificate**
(the appliance's out-of-the-box state), extract the CA cert from the
running `eami-proxy` container once:

```
docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env \
  exec eami-proxy cat /data/caddy/pki/authorities/local/root.crt \
  > eami-collector-ca.pem
```

Then stage `eami-collector-ca.pem` on each target machine (via whatever
mechanism already stages the installer package itself — an SCCM package,
an Intune Win32 app payload, a login script, a shared network path) and
pass its **path** as an extra installer parameter alongside the existing
`COLLECTOR_URL`/`COLLECTOR_API_KEY` ones:

- **Windows (MSI):** `COLLECTOR_CA_CERT_PATH=C:\Staging\eami-collector-ca.pem`
- **Linux (.deb/.rpm):** `EAMI_COLLECTOR_CA_CERT_PATH=/tmp/eami-collector-ca.pem`
- **macOS (.pkg):** `EAMI_COLLECTOR_CA_CERT_PATH=/tmp/eami-collector-ca.pem`
  (or Jamf script parameter `$6`)

See `eami-agent/installer/README.md` for the full per-platform command
examples. The value is a **file path**, never the certificate content
itself, passed directly through an installer property/env var — embedding
multi-line PEM content through an `msiexec`/shell command line has its own
real quoting and length fragility this design deliberately avoids.

**Why this is safe to distribute this way, not a weaker trust model than
the browser-facing default:** the CA cert is the appliance's own *public*
key material — extracting and staging it doesn't expose anything an
attacker could use to impersonate the collector or decrypt traffic (that
would require the *private* key, which never leaves the `caddy_data`
volume). Distributing it via the same channel that already carries the
API key (SCCM/Intune/GPO, all already trusted to deliver credentials to
managed endpoints) is consistent with how those tools are already used in
this deployment model.

### eami-collector authentication — per-agent keys (B-073, done)

B-072 closed the *transport* gap (traffic is now encrypted); B-073 closes
the remaining *identity* gap. Before B-073, every endpoint agent presented
the same single, static `COLLECTOR_API_KEY` value — TLS meant that key
(and the report content it protects) could no longer be read off the wire
by passive network access, but anyone who obtained the key through *any
other* means (a compromised endpoint's own local config/registry, an
insider) could still impersonate any other agent to the collector.

**Mechanism: admin-generated key pool, one key per agent, bound to the
agent's own `agent_id` (its hostname, by default — no installer changes
needed on any platform, since that's already the value every agent
self-reports today).** Chosen over self-enrollment: this appliance's
existing install flow (this section's own CA-cert distribution above,
`COLLECTOR_URL`/`COLLECTOR_API_KEY`) already has an admin generating a
distinct install command per machine, so minting a distinct key per
machine at the same time is a small addition to an existing workflow —
not a new protocol. Self-enrollment would need its own new anti-abuse
mechanism (a time-limited bootstrap window, or an admin-approval queue)
just to avoid *moving* the "anyone can claim to be anyone" problem into a
different form, for no benefit here.

**Minting/revoking, on the appliance itself:**

```
docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env \
  exec eami-collector /app/collector mint-key --agent-id workstation-42 --label "Office floor 3, workstation 42"

docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env \
  exec eami-collector /app/collector revoke-key --agent-id workstation-42

docker compose -f /opt/eami/docker-compose.prod.yml --env-file /data/eami/.env \
  exec eami-collector /app/collector list-keys
```

The printed key is passed as the install command's existing
`COLLECTOR_API_KEY` parameter (Windows MSI) / `EAMI_COLLECTOR_API_KEY` env
var (Linux/macOS postinstall) — the same slot every install already uses,
just no longer one identical value shared fleet-wide.

**A per-agent credential only authenticates as the one agent it was minted
for** — `eami-collector` now checks the report's own self-declared
`agent_id` against the identity bound to whichever credential authenticated
the request, and rejects a mismatch (`403`). Without this check, per-agent
credentials alone would be cosmetic: any agent's key could still submit a
report claiming to be a different agent.

**The legacy static `COLLECTOR_API_KEY` still works, unchanged, if still
configured** — B-073 is additive, not a breaking cutover. A deployment can
migrate machine-by-machine: mint a real per-agent key for each endpoint as
it's reached, and only unset `COLLECTOR_API_KEY` once every machine has
one. A key with no bound identity (the static key) is never checked
against a claimed `agent_id` — that check only applies to per-agent
DB-backed keys.

**Also not built this round, explicitly out of scope per B-071/B-072's own
briefs:** public CA / Let's Encrypt automation (unrealistic default for the
common case of an on-prem appliance with no public hostname) and full
internal mTLS between every service (ADR-020 Model A is a single-tenant,
single-VM appliance — every service already shares one private docker
network with no other tenant on it; an attacker who already has
traffic-sniffing access inside that network has far worse access
available already, e.g. direct Postgres/volume/secret access — mTLS
defends against a zero-trust/multi-tenant threat model this product isn't).

## Known limitations / explicitly out of scope this round

- `.ova` conversion — not attempted; flagged as a fast-follow, per the
  brief's own stated allowance.
- Update-mechanism polish (a CLI/UI to drive "replace disk 1" instead of
  doing it by hand at the hypervisor level) — separate, later work.
- Flatcar — explicitly deferred per founder decision (ADR-020's own
  context); Debian minimal only, this round.
- `GATEWAY_UI_BASE_URL` in the generated `.env` defaults to
  `http://<primary-IP-at-first-boot>` (best-effort `hostname -I`) — used
  only for constructing links in Slack approval notifications, not
  required for the stack to start. An admin with a different reachable
  address should correct it in `/data/eami/.env` and
  `docker compose ... restart eami-gateway`.
- `eami-data-disk.sh` writes `/etc/docker/daemon.json` and
  `/etc/containerd/config.toml` as full overwrites (only the `data-root`/
  `root` key this appliance actually needs), not a merge into whatever
  the packaged defaults already contain. Deliberate, not an oversight:
  this appliance doesn't rely on or expose any other Docker/containerd
  config surface, and a real JSON/TOML-aware merge in pure bash is real
  complexity this narrow, single-purpose use doesn't need. Worth
  revisiting only if a future need arises to set other daemon/containerd
  options on this image.
- ~~No self-service org/admin signup path exists anywhere in `eami-api`
  yet... The next brief (setup wizard) needs its own race-safety design~~
  — **closed.** The setup wizard now exists (`eami-api/internal/api/
  bootstrap.go`, `eami-ui`'s `/setup` route) — see the "First-boot
  detection contract, and the setup wizard" section above for the token
  gate and its DB-transaction-based race-safety design.
