#!/usr/bin/env bash
# =============================================================================
# build.sh — Builds the EAMI Agent macOS .pkg installer
# =============================================================================
#
# Requires: macOS with Xcode Command Line Tools (pkgbuild is part of Xcode CLT)
#
# Usage:
#   ./eami-agent/installer/macos/build.sh [VERSION] [ARCH] [BINARY_PATH] [OUTPUT_DIR]
#
# Arguments:
#   VERSION      Semver string, e.g. 1.2.3 (default: 0.0.0-dev)
#   ARCH         Target arch: amd64 or arm64 (default: amd64)
#   BINARY_PATH  Path to the compiled eami-agent binary for the target arch
#                (default: ../eami-agent-darwin-ARCH relative to this script)
#   OUTPUT_DIR   Directory to write the .pkg to (default: ./dist)
#
# Output:
#   OUTPUT_DIR/eami-agent-VERSION-darwin-ARCH.pkg
#
# Build both arches:
#   ./build.sh 1.0.0 amd64 ../eami-agent-darwin-amd64
#   ./build.sh 1.0.0 arm64 ../eami-agent-darwin-arm64
#
# Silent install (after build):
#   sudo installer -pkg eami-agent-1.0.0-darwin-amd64.pkg -target /
#
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Arguments ────────────────────────────────────────────────────────────────
VERSION="${1:-0.0.0-dev}"
ARCH="${2:-amd64}"
BINARY_PATH="${3:-}"
OUTPUT_DIR="${4:-${SCRIPT_DIR}/dist}"

if [[ -z "$BINARY_PATH" ]]; then
    BINARY_PATH="${SCRIPT_DIR}/../../../eami-agent-darwin-${ARCH}"
fi

# ── Validate ─────────────────────────────────────────────────────────────────
case "$ARCH" in
    amd64) ;;
    arm64) ;;
    *) echo "ERROR: ARCH must be amd64 or arm64, got: ${ARCH}"; exit 1 ;;
esac

if ! command -v pkgbuild &>/dev/null; then
    echo "ERROR: pkgbuild not found."
    echo "  Install Xcode Command Line Tools: xcode-select --install"
    exit 1
fi

if [[ ! -f "$BINARY_PATH" ]]; then
    echo "ERROR: Binary not found at: ${BINARY_PATH}"
    echo ""
    echo "  Build it first (from repo root):"
    echo "    GOOS=darwin GOARCH=${ARCH} CGO_ENABLED=0 \\"
    echo "      go build -ldflags='-w -s' -o eami-agent-darwin-${ARCH} ./cmd/agent/"
    exit 1
fi

PKG_NAME="eami-agent-${VERSION}-darwin-${ARCH}.pkg"
echo "Building: ${PKG_NAME}"
echo "  Binary : ${BINARY_PATH}"
echo "  Output : ${OUTPUT_DIR}/${PKG_NAME}"
echo ""

# ── Build staging area ───────────────────────────────────────────────────────
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

PAYLOAD_DIR="${WORK_DIR}/payload"
SCRIPTS_DIR="${WORK_DIR}/scripts"

# Binary destination: /usr/local/bin/eami-agent
mkdir -p "${PAYLOAD_DIR}/usr/local/bin"
cp "$BINARY_PATH" "${PAYLOAD_DIR}/usr/local/bin/eami-agent"
chmod 755 "${PAYLOAD_DIR}/usr/local/bin/eami-agent"

# launchd plist destination: /Library/LaunchDaemons/io.eami.agent.plist
mkdir -p "${PAYLOAD_DIR}/Library/LaunchDaemons"
cp "${SCRIPT_DIR}/io.eami.agent.plist" \
    "${PAYLOAD_DIR}/Library/LaunchDaemons/io.eami.agent.plist"
chmod 644 "${PAYLOAD_DIR}/Library/LaunchDaemons/io.eami.agent.plist"

# /etc/eami placeholder directory (config written by postinstall)
mkdir -p "${PAYLOAD_DIR}/etc/eami"
chmod 755 "${PAYLOAD_DIR}/etc/eami"

# postinstall script (runs as root after payload is extracted)
mkdir -p "$SCRIPTS_DIR"
cp "${SCRIPT_DIR}/postinstall" "${SCRIPTS_DIR}/postinstall"
chmod 755 "${SCRIPTS_DIR}/postinstall"

# ── Build the .pkg ───────────────────────────────────────────────────────────
mkdir -p "$OUTPUT_DIR"

pkgbuild \
    --root         "$PAYLOAD_DIR" \
    --scripts      "$SCRIPTS_DIR" \
    --identifier   "io.eami.agent" \
    --version      "$VERSION" \
    --install-location "/" \
    "${OUTPUT_DIR}/${PKG_NAME}"

echo ""
echo "Built: ${OUTPUT_DIR}/${PKG_NAME}"
echo ""
echo "Silent install:"
echo "  sudo installer -pkg '${OUTPUT_DIR}/${PKG_NAME}' -target /"
echo ""
echo "Silent install with config (env vars read by postinstall):"
echo "  sudo EAMI_COLLECTOR_URL=https://collector.corp.com:8888 \\"
echo "       EAMI_COLLECTOR_API_KEY=eami_k_abc123 \\"
echo "       installer -pkg '${OUTPUT_DIR}/${PKG_NAME}' -target /"
