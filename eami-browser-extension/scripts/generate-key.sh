#!/bin/sh
# generate-key.sh — generates a stable Chrome extension ID for eami-browser-extension.
#
# Chrome/Edge extension IDs are normally random (path-dependent) when an
# unpacked extension is loaded without a manifest "key" field, and change
# every time the extension folder moves. For a native-messaging host
# (B0's eami-agent), the manifest's allowed_origins must name a FIXED
# extension ID up front -- so this extension embeds an RSA public key in
# manifest.json's "key" field instead, which makes Chrome derive a stable,
# deterministic ID from that key (independent of file path, and usable
# before -- and after -- any eventual Chrome Web Store publication, which
# assigns its own separate permanent ID and does not read "key" at all).
#
# Algorithm (documented Chrome behaviour, not guessed):
#   1. SHA-256 hash the DER-encoded SubjectPublicKeyInfo (the public key
#      bytes manifest.json's "key" field carries, base64-decoded).
#   2. Take the first 16 bytes (32 hex characters) of that hash.
#   3. Map each hex nibble 0-9,a-f to the letters a-p (0->a, 1->b, ...,
#      9->j, a->k, ..., f->p).
#   4. The resulting 32-character string is the extension ID.
#
# Run: sh scripts/generate-key.sh
# Writes:
#   keys-local/private-key.pem   (NEVER commit -- gitignored; only needed
#                                  if you ever have to regenerate/re-derive
#                                  the same ID; not required for Chrome to
#                                  load or run the extension)
#   keys-local/public-key.der    (the raw bytes the ID/manifest key are derived from)
# Prints the base64 "key" value (paste into manifest.json) and the
# resulting extension ID.

set -e

DIR="$(dirname "$0")/../keys-local"
mkdir -p "$DIR"

PRIVATE_KEY="$DIR/private-key.pem"
PUBLIC_DER="$DIR/public-key.der"

if [ ! -f "$PRIVATE_KEY" ]; then
  openssl genrsa -out "$PRIVATE_KEY" 2048 2>/dev/null
  echo "Generated new private key: $PRIVATE_KEY (kept local, gitignored)"
else
  echo "Reusing existing private key: $PRIVATE_KEY"
fi

openssl rsa -in "$PRIVATE_KEY" -pubout -outform DER -out "$PUBLIC_DER" 2>/dev/null

KEY_B64=$(openssl base64 -A -in "$PUBLIC_DER")
HASH_HEX=$(openssl dgst -sha256 "$PUBLIC_DER" | awk '{print $NF}')
ID_HEX_PREFIX=$(echo "$HASH_HEX" | cut -c1-32)
EXTENSION_ID=$(echo "$ID_HEX_PREFIX" | tr '0123456789abcdef' 'abcdefghijklmnop')

echo ""
echo "manifest.json \"key\" value (paste in as-is):"
echo "$KEY_B64"
echo ""
echo "Computed extension ID (this is what goes into B0's manifest allowed_origins):"
echo "$EXTENSION_ID"
