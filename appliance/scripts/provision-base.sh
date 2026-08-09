#!/usr/bin/env bash
# =============================================================================
# EAMI appliance — base OS provisioning (runs once, during the Packer build)
# =============================================================================
# Installs Docker, installs the appliance's own systemd units/scripts (already
# copied into place by Packer's file provisioner before this runs), and --
# critically -- locks the image down before Packer captures it: no ssh, no
# usable local login, no cloud-init on future boots. This is what actually
# makes "SSH disabled by default" true in the shipped image, not just true
# during the build.
# =============================================================================
set -euo pipefail

log() { echo "[provision-base] $*"; }

# ── Docker Engine + Compose plugin (official apt repo, matches upstream
#    guidance -- not the distro's older/slower-to-update docker.io package) ──
log "Installing Docker Engine + compose plugin, plus parted (data-disk partitioning)"
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg parted

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc

. /etc/os-release
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian ${VERSION_CODENAME} stable" \
  > /etc/apt/sources.list.d/docker.list

apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin

systemctl enable docker.service

# ── Appliance scripts + systemd units ───────────────────────────────────────
# (files themselves were already placed by Packer's file provisioner at
# /opt/eami-appliance/{scripts,systemd} -- this step just wires them in)
log "Installing eami-data-disk.service / eami-stack.service"
install -m 0755 /opt/eami-appliance/scripts/eami-data-disk.sh /usr/local/sbin/eami-data-disk.sh
install -m 0755 /opt/eami-appliance/scripts/eami-stack.sh /usr/local/sbin/eami-stack.sh
install -m 0644 /opt/eami-appliance/systemd/eami-data-disk.service /etc/systemd/system/eami-data-disk.service
install -m 0644 /opt/eami-appliance/systemd/eami-stack.service /etc/systemd/system/eami-stack.service

systemctl daemon-reload
systemctl enable eami-data-disk.service
systemctl enable eami-stack.service

# ── The actual application bundle (docker-compose.prod.yml, schema
#    migrations, backup scripts) lives at /opt/eami -- copied verbatim from
#    this repo by Packer's file provisioner, never edited by this script. ──
mkdir -p /opt/eami
log "Application bundle present at /opt/eami:"
ls -la /opt/eami

# ── Lock down the image before it's captured ────────────────────────────────
# Every account's password is locked (no password login at all, console or
# network). GRUB itself is left unprotected -- physical/console access to
# the bootloader is the documented emergency-recovery path (interrupt GRUB,
# boot to a rescue/single-user shell). This is a deliberate, documented
# choice (appliance/README.md), not an oversight: it avoids shipping any
# fixed/guessable emergency credential while still leaving a real recovery
# path for someone with physical or hypervisor-console access.
log "Locking all local account passwords"
awk -F: '{print $1}' /etc/passwd | while read -r u; do passwd -l "$u" >/dev/null 2>&1 || true; done

log "Disabling ssh.service for all future boots (this build's own SSH session stays alive -- disable, not --now, doesn't kill it)"
systemctl disable ssh.service

log "Disabling cloud-init for all future boots (build-time provisioning tool only, not part of the shipped appliance)"
systemctl disable cloud-init.service cloud-init-local.service cloud-config.service cloud-final.service 2>/dev/null || true
touch /etc/cloud/cloud-init.disabled
cloud-init clean --logs --seed || true

# Version marker -- proves a rebuilt image is genuinely a new build, not a
# stale reused one (used by B-053's own AC6 verification: this file's
# content differs across rebuilds, confirming the OS disk actually changed
# while paired data-disk content is separately checked to have survived).
date -u '+%Y-%m-%dT%H:%M:%SZ' > /etc/eami-appliance-build-date

# ── Strip the packer build account's ability to authenticate, without
#    deleting the account itself ──────────────────────────────────────────
# An earlier version of this script fully deleted the `packer` account
# (userdel) here. Reverted: Packer's own `shutdown_command` (see the
# .pkr.hcl source block) needs a working `sudo -n` for this exact account
# to cleanly power the VM off -- Packer's QEMU builder does not wait for a
# guest-initiated shutdown when shutdown_command is unset, it force-kills
# the VM process the instant this provisioner returns. A prior attempt to
# self-shutdown from inside this script (backgrounded, after deleting the
# account) raced that force-kill and risked capturing a disk image from an
# unclean shutdown (unflushed writes, un-journaled ext4 state) on every
# single build -- a far worse outcome than the residual this section
# accepts instead: the account structurally still exists in the shipped
# image, but is unreachable -- password locked (above), authorized_keys
# stripped (below) so even a future accidental `systemctl start ssh`
# can't be reached with this build's own leftover key, and ssh.service
# itself disabled. The passwordless sudo grant remains solely so Packer's
# own shutdown_command can use it during THIS build; it is never reachable
# post-boot since nothing can authenticate as `packer` at all by then.
log "Stripping packer account authorized_keys (password already locked above)"
rm -f /home/packer/.ssh/authorized_keys

log "provision-base.sh complete"
