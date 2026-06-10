#!/usr/bin/env bash
# Headless coverage gate: build the binary, run the driver, assert that every
# command and flag was exercised. Exits nonzero on any gap. This is what CI runs.
#
#   ./run.sh            # quiet pacing, full run + assertion
#   PACE=0.4 ./run.sh   # watchable pacing (for recording / the UI)
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RUN_DIR="$DIR/.run"
export NDJSON="$RUN_DIR/events.ndjson"
mkdir -p "$RUN_DIR/bin"

for t in go jq sqlite3 ssh-keygen curl; do
  command -v "$t" >/dev/null || { echo "run.sh: missing dependency: $t" >&2; exit 2; }
done

echo "Building store binary ..."
( cd "$DIR/.." && go build -o "$RUN_DIR/bin/store" ./cmd/store )

cleanup() { bash "$DIR/teardown.sh" >/dev/null 2>&1 || true; }
trap cleanup EXIT

PACE="${PACE:-0}" bash "$DIR/driver.sh" 2>&1 | tee "$RUN_DIR/driver.log"

bash "$DIR/assert_coverage.sh"
