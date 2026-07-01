#!/usr/bin/env bash
# generate-api-client.sh — Generates the typed TypeScript API client for eami-ui
#
# Runs openapi-typescript against api/openapi.yaml and writes the generated
# types to eami-ui/src/api/schema.d.ts.
#
# Usage (from repo root):
#   ./scripts/generate-api-client.sh
#
# Called automatically by the eami-ui build process:
#   npm run generate-client   (defined in eami-ui/package.json)
#
# Prerequisites:
#   Node.js 20+ with npx available
#   api/openapi.yaml must exist (source of truth for all API shapes)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SPEC="$REPO_ROOT/api/openapi.yaml"
OUT="$REPO_ROOT/eami-ui/src/api/schema.d.ts"

if [[ ! -f "$SPEC" ]]; then
    echo "ERROR: OpenAPI spec not found at: $SPEC"
    echo "  The spec is owned by Architect-EAMI. Check api/openapi.yaml exists."
    exit 1
fi

mkdir -p "$(dirname "$OUT")"

echo "Generating TypeScript client from $SPEC ..."
npx --yes openapi-typescript "$SPEC" --output "$OUT"

echo "Done: $OUT"
