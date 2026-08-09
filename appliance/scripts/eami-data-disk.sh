#!/usr/bin/env bash
# =============================================================================
# EAMI appliance — data disk provisioning (runs at every boot, via
# eami-data-disk.service, Before=docker.service)
# =============================================================================
# Idempotent: if the data disk is already partitioned/formatted/mounted (the
# normal case on every boot after the first), this is a fast no-op. Only a
# genuinely fresh, unformatted second disk gets partitioned -- this script
# never reformats a disk that already carries the EAMIDATA label, so it's
# safe to run on every boot without risking the 5 named volumes' data.
#
# Deployment contract (documented in appliance/README.md): the appliance
# expects a SECOND virtio disk attached at /dev/vdb, separate from the OS
# disk (/dev/vda). This is what makes "rebuild/replace the OS image, data
# survives" true -- an OS update swaps /dev/vda's backing image; /dev/vdb
# is attached to the new VM unchanged.
# =============================================================================
set -euo pipefail

log() { echo "[eami-data-disk] $*"; }

DATA_DISK="/dev/vdb"
DATA_LABEL="EAMIDATA"
DATA_MOUNT="/data"

if [[ ! -b "$DATA_DISK" ]]; then
    echo "[eami-data-disk] FATAL: ${DATA_DISK} not found. This appliance requires a second" >&2
    echo "  virtio disk attached separately from the OS disk -- see README.md." >&2
    exit 1
fi

mkdir -p "$DATA_MOUNT"

# Already labeled from a prior boot: just ensure it's mounted, done.
# Checks the specific partition (${DATA_DISK}1), not `blkid --label` alone
# -- that searches every block device system-wide, so a stray EAMIDATA
# label on some OTHER disk (e.g. a data disk salvaged from a different
# EAMI appliance instance) would make this script skip formatting the
# real /dev/vdb entirely, and the LABEL-based fstab mount below would then
# resolve to whichever device blkid found first -- silently mounting the
# wrong data disk instead of failing loudly like the signature guard below
# does for the unrecognized-disk case.
FRESH_FORMAT=0
if [[ "$(blkid -o value -s LABEL "${DATA_DISK}1" 2>/dev/null)" == "$DATA_LABEL" ]]; then
    log "${DATA_LABEL} filesystem already exists on ${DATA_DISK}1 -- skipping partition/format"
else
    FRESH_FORMAT=1
    log "No ${DATA_LABEL} filesystem found on ${DATA_DISK} -- partitioning as fresh data disk"
    # Refuse to touch a disk that has ANY existing partition table or
    # filesystem signature, even one we don't recognize -- fail loud rather
    # than silently wiping something. wipefs -n is a dry-run probe.
    if wipefs -n "$DATA_DISK" 2>/dev/null | grep -q .; then
        echo "[eami-data-disk] FATAL: ${DATA_DISK} already has a filesystem/partition signature" >&2
        echo "  that isn't labeled ${DATA_LABEL}. Refusing to auto-partition a disk that might" >&2
        echo "  hold real data under an unexpected label. Inspect manually: wipefs -n ${DATA_DISK}" >&2
        exit 1
    fi

    parted -s "$DATA_DISK" mklabel gpt mkpart primary ext4 0% 100%
    udevadm settle
    mkfs.ext4 -q -L "$DATA_LABEL" "${DATA_DISK}1"
    log "Formatted ${DATA_DISK}1 as ext4, label=${DATA_LABEL}"
fi

if ! grep -q "^LABEL=${DATA_LABEL}[[:space:]]" /etc/fstab 2>/dev/null; then
    echo "LABEL=${DATA_LABEL}  ${DATA_MOUNT}  ext4  defaults,nofail  0  2" >> /etc/fstab
    log "Added ${DATA_MOUNT} to /etc/fstab"
fi

if [[ "$FRESH_FORMAT" == "1" ]]; then
    # The fstab entry was just added this same run -- nothing else races to
    # mount it yet (systemd's fstab-generator already ran earlier in boot,
    # before this entry existed), so there's nothing to poll for. Mount it
    # ourselves directly.
    mount -a
else
    # On any boot after the first, systemd's own fstab-generated data.mount
    # unit is *also* racing to mount /data concurrently with this script,
    # since the fstab entry from a prior boot is now permanent (found live:
    # a single check-then-mount wasn't enough -- mount -a's own errors and
    # systemd's generated unit can genuinely land on either side of a single
    # check depending on scheduling, not just deterministically "already
    # mounted"). Poll briefly for either side to finish before trying
    # ourselves, and tolerate mount -a's own error if something else won.
    for _ in $(seq 1 10); do
        mountpoint -q "$DATA_MOUNT" && break
        sleep 1
    done
    if ! mountpoint -q "$DATA_MOUNT"; then
        mount -a || true
    fi
fi
mountpoint -q "$DATA_MOUNT" || { echo "[eami-data-disk] FATAL: ${DATA_MOUNT} did not mount" >&2; exit 1; }
log "${DATA_MOUNT} mounted"

# ── Point Docker's own storage (images, containers, AND every named volume
#    docker-compose.prod.yml declares) at the data disk. This is the
#    mechanism that keeps the 5 named volumes off the OS disk WITHOUT
#    modifying docker-compose.prod.yml itself -- Docker creates named
#    volumes under whatever data-root is configured, transparently. ──
mkdir -p "${DATA_MOUNT}/docker"
DAEMON_JSON=/etc/docker/daemon.json
DESIRED_DATA_ROOT="${DATA_MOUNT}/docker"

mkdir -p /etc/docker
if [[ -f "$DAEMON_JSON" ]] && grep -q "\"data-root\"[[:space:]]*:[[:space:]]*\"${DESIRED_DATA_ROOT}\"" "$DAEMON_JSON" 2>/dev/null; then
    log "daemon.json already points data-root at ${DESIRED_DATA_ROOT}"
else
    log "Writing ${DAEMON_JSON} (data-root=${DESIRED_DATA_ROOT})"
    cat > "$DAEMON_JSON" <<EOF
{
  "data-root": "${DESIRED_DATA_ROOT}"
}
EOF
fi

# ── containerd has its OWN separate persistent root (/var/lib/containerd
#    by default), independent of Docker's data-root above -- docker-ce on
#    Debian talks to the system containerd.service over a socket, it does
#    NOT embed its own. Found live, the hard way: leaving containerd's root
#    on the OS disk meant an OS-image update (fresh containerd state) paired
#    with Docker's persisted /data/docker container metadata (referencing
#    containerd snapshots that no longer existed) produced "RWLayer of
#    container ... is unexpectedly nil" -- Docker's own volumes/images
#    survived the update, but already-created containers couldn't restart.
#    Redirecting containerd's root here too keeps both engines' state
#    consistent across every OS update, not just Docker's own half of it. ──
mkdir -p "${DATA_MOUNT}/containerd"
CONTAINERD_CONF=/etc/containerd/config.toml
DESIRED_CONTAINERD_ROOT="${DATA_MOUNT}/containerd"

mkdir -p /etc/containerd
if [[ -f "$CONTAINERD_CONF" ]] && grep -q "^root = \"${DESIRED_CONTAINERD_ROOT}\"" "$CONTAINERD_CONF" 2>/dev/null; then
    log "containerd config.toml already points root at ${DESIRED_CONTAINERD_ROOT}"
else
    log "Writing ${CONTAINERD_CONF} (root=${DESIRED_CONTAINERD_ROOT})"
    cat > "$CONTAINERD_CONF" <<EOF
root = "${DESIRED_CONTAINERD_ROOT}"
state = "/run/containerd"
version = 2
EOF
fi

log "eami-data-disk complete"
