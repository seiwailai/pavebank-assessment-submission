#!/usr/bin/env bash
set -euo pipefail

FEES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$FEES_DIR"

TAGS="${1:-${UAT_TAGS:-~@manual}}"
FEATURES="${2:-${UAT_FEATURES:-features}}"
REUSE_EXISTING="${3:-${UAT_REUSE_EXISTING:-0}}"

args=(
  -v
  ./uat
  -run TestUAT
  -count=1
  -args
  -uat-tags="$TAGS"
  -uat-features="$FEATURES"
)

if [[ "$REUSE_EXISTING" != "1" && "$REUSE_EXISTING" != "true" ]]; then
  args+=(-uat-manage-runtime)
fi

go test "${args[@]}"
