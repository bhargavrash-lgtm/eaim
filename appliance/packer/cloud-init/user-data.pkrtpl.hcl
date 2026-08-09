#cloud-config
# ─────────────────────────────────────────────────────────────────────────
# BUILD-TIME ONLY. This seed gives Packer's own provisioning connection a
# way in (SSH key for the `packer` user, sudo). It is never present in the
# shipped image: appliance/scripts/provision-base.sh's last step disables
# ssh.service, locks every account's password, and disables cloud-init
# itself for all future boots -- so this file's effect is fully undone
# before the image is captured. See appliance/README.md's "Build-time
# access, and why it doesn't ship" section.
# ─────────────────────────────────────────────────────────────────────────
users:
  - name: packer
    groups: sudo
    shell: /bin/bash
    sudo: ["ALL=(ALL) NOPASSWD:ALL"]
    lock_passwd: true
    ssh_authorized_keys:
      - ${ssh_public_key}

ssh_pwauth: false
disable_root: true

# Debian's generic cloud image ships with a default 'debian' user via
# cloud-init's own datasource config; this build doesn't rely on it --
# 'packer' above is deliberately the only account driven by this seed.
