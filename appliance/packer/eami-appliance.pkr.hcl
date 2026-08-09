packer {
  required_plugins {
    qemu = {
      version = ">= 1.1.0"
      source  = "github.com/hashicorp/qemu"
    }
    sshkey = {
      version = ">= 1.0.3"
      source  = "github.com/ivoronin/sshkey"
    }
  }
}

# ─────────────────────────────────────────────────────────────────────────
# Base image: Debian 12 (bookworm) official generic cloud qcow2. Chosen
# over ISO+preseed -- far more reliable for unattended Packer builds (no
# boot_command/keyboard-driven installer to keep in sync with Debian
# installer UI changes), and matches scripts/setup.sh's own explicitly
# tested platform list ("Tested: ... Debian 12").
#
# iso_checksum uses Debian's own published SHA512SUMS -- Packer fetches
# and verifies against it directly (file:// checksum-file support), not a
# hand-copied hash that could drift stale.
# ─────────────────────────────────────────────────────────────────────────
variable "debian_cloud_image_url" {
  type    = string
  default = "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2"
}

variable "debian_cloud_image_checksum_url" {
  type    = string
  default = "file:https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS"
}

variable "disk_size" {
  type    = string
  default = "8192M"
}

variable "memory" {
  type    = number
  default = 2048
}

variable "cpus" {
  type    = number
  default = 2
}

variable "headless" {
  type    = bool
  default = true
}

# Ephemeral, Packer-managed SSH keypair for the build connection only.
# Never written to the final image -- provision-base.sh's last step strips
# authorized_keys and disables ssh.service before Packer captures the disk.
data "sshkey" "install" {
  type = "ed25519"
}

locals {
  repo_root      = "${path.root}/../.."
  appliance_root = "${path.root}/.."
}

source "qemu" "eami-appliance" {
  iso_url          = var.debian_cloud_image_url
  iso_checksum     = var.debian_cloud_image_checksum_url
  disk_image       = true

  output_directory = "output-eami-appliance"
  vm_name          = "eami-appliance.qcow2"
  format           = "qcow2"

  accelerator    = "kvm"
  disk_size      = var.disk_size
  memory         = var.memory
  cpus           = var.cpus
  headless       = var.headless
  disk_interface = "virtio"
  net_device     = "virtio-net"

  # NoCloud cloud-init seed, built into an ISO by Packer itself and attached
  # as a second cdrom -- the standard, supported way to bootstrap SSH access
  # into an official cloud image. cd_content (not cd_files) so user-data can
  # be rendered inline via templatefile() with this build's own ephemeral
  # public key, with no separate pre-render step needed before `packer build`.
  cd_content = {
    "meta-data" = file("${path.root}/cloud-init/meta-data")
    "user-data" = templatefile("${path.root}/cloud-init/user-data.pkrtpl.hcl", {
      ssh_public_key = data.sshkey.install.public_key
    })
  }
  cd_label = "cidata"

  communicator          = "ssh"
  ssh_username           = "packer"
  ssh_private_key_file   = data.sshkey.install.private_key_path
  ssh_timeout            = "10m"
  # false deliberately: provision-base.sh strips the packer account's
  # authorized_keys itself, as a controlled, final step -- ssh_clear_
  # authorized_keys would be redundant with that, not a substitute for it.
  ssh_clear_authorized_keys = false

  # shutdown_command is required, not optional, for a correct capture: the
  # QEMU builder does not wait for or detect a guest-initiated shutdown when
  # this is unset -- it force-kills the VM process the instant the last
  # provisioner returns. An earlier version of this template removed
  # shutdown_command (reasoning: it runs as a fresh `sudo` step after every
  # provisioner, which broke once provision-base.sh fully deleted the
  # packer account) and had provision-base.sh self-shutdown instead --
  # that self-shutdown raced Packer's own force-kill and risked capturing
  # an uncleanly-shut-down disk (unflushed writes, un-journaled ext4 state)
  # on every single build. Reverted: provision-base.sh no longer deletes
  # the account (see its own comment on that trade-off), so `sudo -n` still
  # works here, and this is a real Packer-orchestrated clean shutdown that
  # Packer waits for before capturing the disk.
  shutdown_command = "sudo -n shutdown -P now"
  shutdown_timeout = "5m"
}

build {
  name    = "eami-appliance"
  sources = ["source.qemu.eami-appliance"]

  # ── Application bundle (read-only per this brief's own scope: these are
  #    the real repo files, copied verbatim, never rewritten). Uploaded to
  #    /tmp first -- the unprivileged `packer` SSH user can't scp directly
  #    into a root-owned /opt path -- then sudo-moved into place below. ──
  provisioner "shell" {
    inline = ["mkdir -p /tmp/eami-stage"]
  }

  provisioner "file" {
    source      = "${local.repo_root}/docker-compose.prod.yml"
    destination = "/tmp/eami-stage/docker-compose.prod.yml"
  }

  provisioner "file" {
    source      = "${local.repo_root}/schema/migrations-v2"
    destination = "/tmp/eami-stage/migrations-v2"
  }

  provisioner "file" {
    source      = "${local.repo_root}/scripts/backup-db.sh"
    destination = "/tmp/eami-stage/backup-db.sh"
  }

  provisioner "file" {
    source      = "${local.repo_root}/scripts/backup-entrypoint.sh"
    destination = "/tmp/eami-stage/backup-entrypoint.sh"
  }

  provisioner "file" {
    source      = "${local.appliance_root}/scripts/eami-data-disk.sh"
    destination = "/tmp/eami-stage/eami-data-disk.sh"
  }

  provisioner "file" {
    source      = "${local.appliance_root}/scripts/eami-stack.sh"
    destination = "/tmp/eami-stage/eami-stack.sh"
  }

  provisioner "file" {
    source      = "${local.appliance_root}/systemd/eami-data-disk.service"
    destination = "/tmp/eami-stage/eami-data-disk.service"
  }

  provisioner "file" {
    source      = "${local.appliance_root}/systemd/eami-stack.service"
    destination = "/tmp/eami-stage/eami-stack.service"
  }

  provisioner "file" {
    source      = "${local.appliance_root}/scripts/provision-base.sh"
    destination = "/tmp/eami-stage/provision-base.sh"
  }

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /opt/eami/schema /opt/eami/scripts /opt/eami-appliance/scripts /opt/eami-appliance/systemd",
      "sudo mv /tmp/eami-stage/docker-compose.prod.yml /opt/eami/docker-compose.prod.yml",
      "sudo mv /tmp/eami-stage/migrations-v2 /opt/eami/schema/migrations-v2",
      "sudo mv /tmp/eami-stage/backup-db.sh /opt/eami/scripts/backup-db.sh",
      "sudo mv /tmp/eami-stage/backup-entrypoint.sh /opt/eami/scripts/backup-entrypoint.sh",
      "sudo mv /tmp/eami-stage/eami-data-disk.sh /opt/eami-appliance/scripts/eami-data-disk.sh",
      "sudo mv /tmp/eami-stage/eami-stack.sh /opt/eami-appliance/scripts/eami-stack.sh",
      "sudo mv /tmp/eami-stage/eami-data-disk.service /opt/eami-appliance/systemd/eami-data-disk.service",
      "sudo mv /tmp/eami-stage/eami-stack.service /opt/eami-appliance/systemd/eami-stack.service",
      "sudo mv /tmp/eami-stage/provision-base.sh /usr/local/sbin/provision-base.sh",
      "sudo chmod +x /usr/local/sbin/provision-base.sh",
      "sudo rmdir /tmp/eami-stage",
      # `mv` preserves the uploading (unprivileged `packer`) user as owner --
      # only the containing directories, made via `sudo mkdir`, end up
      # root-owned. Harmless in practice (root-run systemd services/docker
      # compose don't care about file owner given default world-readable
      # perms), but inconsistent with provision-base.sh's own root-owned
      # /usr/local/sbin/*, and worth closing now that the packer account
      # itself gets deleted by provision-base.sh anyway.
      "sudo chown -R root:root /opt/eami /opt/eami-appliance",
      # provision-base.sh removes itself AND the packer account as its own
      # last acts (that ordering matters -- see its own comment); no
      # cleanup command can run after it here, since by the time it
      # returns, `sudo` no longer recognizes this session's user at all.
      "sudo /usr/local/sbin/provision-base.sh",
    ]
  }
}
