#!/usr/bin/env bash
# Assert that the driver exercised every command+flag in the manifest.
# Reads the NDJSON event stream, unions all step.ids, and diffs against
# manifest.json. Prints a grouped report and exits nonzero on any gap.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NDJSON="${NDJSON:-$DIR/.run/events.ndjson}"
MANIFEST="$DIR/manifest.json"

command -v jq >/dev/null || { echo "assert_coverage: jq is required" >&2; exit 2; }
[ -f "$NDJSON" ]   || { echo "assert_coverage: no events file: $NDJSON" >&2; exit 2; }
[ -f "$MANIFEST" ] || { echo "assert_coverage: no manifest: $MANIFEST" >&2; exit 2; }

if [ -t 1 ]; then GRN=$(tput setaf 2); RED=$(tput setaf 1); BOLD=$(tput bold); RST=$(tput sgr0)
else GRN=""; RED=""; BOLD=""; RST=""; fi

covset="$(mktemp)"; trap 'rm -f "$covset"' EXIT
jq -r 'select(.t=="step") | .ids[]' "$NDJSON" | sort -u > "$covset"

total=$(jq 'length' "$MANIFEST")
covered=0; missing=0

printf '\n%s== Coverage report ==%s\n' "$BOLD" "$RST"
# manifest in display order, grouped
jq -r '.[] | "\(.order)\t\(.group)\t\(.id)"' "$MANIFEST" | sort -n -k1 | {
  lastg=""
  while IFS=$'\t' read -r _ group id; do
    if [ "$group" != "$lastg" ]; then printf '\n%s%s%s\n' "$BOLD" "$group" "$RST"; lastg="$group"; fi
    if grep -qxF "$id" "$covset"; then
      printf '  %s✓%s store %s\n' "$GRN" "$RST" "$id"
    else
      printf '  %s✗ store %s  (never exercised)%s\n' "$RED" "$id" "$RST"
    fi
  done
}

# recompute counts outside the subshell
while IFS= read -r id; do
  if grep -qxF "$id" "$covset"; then covered=$((covered+1)); else missing=$((missing+1)); fi
done < <(jq -r '.[].id' "$MANIFEST")

printf '\n%sCoverage: %d / %d units exercised%s\n' "$BOLD" "$covered" "$total" "$RST"
if [ "$missing" -gt 0 ]; then
  printf '%sFAIL: %d unit(s) never exercised.%s\n' "$RED" "$missing" "$RST" >&2
  exit 1
fi
printf '%sPASS: every command and flag was exercised.%s\n' "$GRN" "$RST"
