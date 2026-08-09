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

## First-boot detection contract (for the future setup wizard)

`/data/eami/state` contains exactly one of:

- `unconfigured` — written once, on the stack's first successful bring-up.
  No org/admin exists in the database (this appliance deliberately never
  runs `scripts/setup.sh`'s interactive seeding step — AC3).
- `configured` — **not written by anything in this brief.** The future
  setup-wizard brief is expected to write this value after it creates the
  first org/admin, as the signal that setup is complete. This brief only
  establishes the file and its `unconfigured` starting value — designed
  now, per the task brief's own instruction, even though the wizard isn't
  built yet.

Whatever serves the wizard (an `eami-api` endpoint, most likely, added in
that later brief) reads this file directly from the host filesystem. No
API endpoint exists for it yet — deliberately: `application code` is out
of this brief's `MUST NOT MODIFY` scope.

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
- No self-service org/admin signup path exists anywhere in `eami-api`
  yet, so today's empty-`orgs`-table first-boot state has no race to
  worry about (nothing can claim the first org over the network at all).
  **The next brief (setup wizard) needs its own race-safety design** for
  whatever mechanism lets the first visitor claim admin — flagged here
  per this brief's own required first-boot auth-gap threat modeling, not
  solved here (no such mechanism exists yet to make race-safe).
