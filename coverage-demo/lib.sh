#!/usr/bin/env bash
# Shared config + helpers for the AgentStore COVERAGE demo.
#
# Unlike the narrative reference demo, this run's job is exhaustiveness: invoke
# every `store` command and flag at least once. Each invocation that counts for
# coverage is recorded as one NDJSON line (the "events" stream) in addition to
# the normal human-readable output (the "terminal" stream). The React viewer
# tails the NDJSON to tick checkboxes; CI diffs it against the manifest.
#
# Sourced by driver.sh and the step scripts. Not meant to be run directly.

set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${AGENTSTORE_REPO:-$(cd "$DEMO_DIR/.." && pwd)}"   # the agentstore repo (this dir's parent)
RUN_DIR="${RUN_DIR:-$DEMO_DIR/.run}"
BIN="$RUN_DIR/bin/store"
NDJSON="${NDJSON:-$RUN_DIR/events.ndjson}"

MAIN_ADDR="127.0.0.1:8080"
MOVE_ADDR="127.0.0.1:8081"
MAIN_URL="http://$MAIN_ADDR"
MOVE_URL="http://$MOVE_ADDR"
REPO_NAME="workspace"
ACTORS=(human research-agent writer-agent graph-layer)

HOMES_DIR="$RUN_DIR/homes"
SERVERS_DIR="$RUN_DIR/servers"

# --- formatting ---
# Real TTY -> use terminfo. Not a TTY but FORCE_COLOR set (e.g. streamed into the
# UI's xterm) -> emit raw ANSI. Otherwise plain.
if [ -t 1 ]; then
  BOLD=$(tput bold); DIM=$(tput dim); RST=$(tput sgr0)
  GRN=$(tput setaf 2); CYN=$(tput setaf 6); RED=$(tput setaf 1); YEL=$(tput setaf 3)
  GRAY=$(tput setaf 8 2>/dev/null || true); [ -n "$GRAY" ] || GRAY="$DIM"
elif [ -n "${FORCE_COLOR:-}" ]; then
  BOLD=$'\e[1m'; DIM=$'\e[2m'; RST=$'\e[0m'
  GRN=$'\e[32m'; CYN=$'\e[36m'; RED=$'\e[31m'; YEL=$'\e[33m'; GRAY=$'\e[90m'
else
  BOLD=""; DIM=""; RST=""; GRN=""; CYN=""; RED=""; YEL=""; GRAY=""
fi
PACE="${PACE:-0}"
pace()  { case "$PACE" in 0 | "") ;; *) sleep "$PACE" ;; esac; }

say()  { printf "\n%s== %s ==%s\n" "$BOLD$CYN" "$*" "$RST"; pace; }
note() { printf "%s# %s%s\n" "$DIM" "$*" "$RST"; pace; }
ok()   { printf "%s✓ %s%s\n" "$GRN" "$*" "$RST"; pace; }
warn() { printf "%s! %s%s\n" "$YEL" "$*" "$RST"; }
die()  { printf "%s✗ %s%s\n" "$RED" "$*" "$RST" >&2; exit 1; }

prompt() {
  printf "\n%s%s\$%s %sstore " "$BOLD" "$GRN" "$RST" "$BOLD"
  printf "%s" "$*"
  printf "%s\n" "$RST"
  pace
}

# --- NDJSON coverage stream ---------------------------------------------------
# emit appends one JSON line to the events file (the second, machine stream).
emit() { [ -n "${NDJSON:-}" ] && printf '%s\n' "$1" >> "$NDJSON"; return 0; }

json_str() { local s=${1//\\/\\\\}; s=${s//\"/\\\"}; printf '"%s"' "$s"; }
json_ids() {
  local IFS=','; local arr=($1); local out="" a
  for a in "${arr[@]}"; do [ -n "$a" ] && out+="${out:+,}$(json_str "$a")"; done
  printf '[%s]' "$out"
}
# record coverage for one invocation: emit_step "<ids,csv>" <exit> "<display>"
emit_step() {
  emit "{\"t\":\"step\",\"ids\":$(json_ids "$1"),\"exit\":$2,\"cmd\":$(json_str "store $3")}"
}

# --- running store ------------------------------------------------------------
home() { echo "$HOMES_DIR/$1"; }

# Run the binary as <actor> from working dir <dir>: as <actor> <dir> <args...>
as() {
  local actor="$1" dir="$2"; shift 2
  ( cd "$dir" && HOME="$(home "$actor")" "$BIN" "$@" )
}

# cmd echoes the invocation then runs it with output visible (returns its status).
cmd() {
  local actor="$1" dir="$2"; shift 2
  local disp="" a
  for a in "$@"; do
    case "$a" in *" "*) disp+=" \"$a\"" ;; *) disp+=" $a" ;; esac
  done
  prompt "${disp# }"
  as "$actor" "$dir" "$@"
}

# step = the coverage primitive: run a foreground `store` invocation and record
# the manifest ids it covers. Tolerates nonzero exit (negative tests are
# expected to fail) — coverage cares that the command ran, not that it succeeded.
#   step <actor> <dir> "<ids,csv>" -- <store args...>
step() {
  local actor="$1" dir="$2" ids="$3"; shift 3
  [ "${1:-}" = "--" ] && shift
  local disp="" a; for a in "$@"; do case "$a" in *" "*) disp+=" \"$a\"";; *) disp+=" $a";; esac; done
  local rc=0
  cmd "$actor" "$dir" "$@" || rc=$?
  emit_step "$ids" "$rc" "${disp# }"
  return 0
}

# mark = record coverage for an invocation run by hand (background watch, piped
# stdin, etc.) that `step` can't wrap. mark "<ids,csv>" <exit> "<display>"
mark() { emit_step "$1" "$2" "$3"; }

# scaffold = a NON-coverage setup command (seeding, extra grants, the commits and
# pushes between coverage steps). Echoes the invocation in gray so it is clearly
# secondary to the green coverage prompts, runs it quietly, and never aborts the
# run. Not recorded for coverage.  scaffold <actor> <dir> <store args...>
scaffold() {
  local actor="$1" dir="$2"; shift 2
  local disp="" a
  for a in "$@"; do case "$a" in *" "*) disp+=" \"$a\"" ;; *) disp+=" $a" ;; esac; done
  printf "%s\$ store%s%s\n" "$GRAY" "$disp" "$RST"
  as "$actor" "$dir" "$@" >/dev/null 2>&1 || true
}

# edit_doc appends a line to an EXISTING tracked file and narrates the edit, so
# the demo visibly *modifies* content (not just creates files).
#   edit_doc <repo-root> <repo-relative-path> <line>
edit_doc() {
  printf '%s\n' "$3" >> "$1/$2"
  note "edit $2 — appended: \"$3\""
}

# --- misc helpers -------------------------------------------------------------
tracked() { find "$1" -type f -not -path '*/.agentstore/*' 2>/dev/null | sed "s#$1/##" | sort; }
max_seq() { sqlite3 "$1/.agentstore/store.db" "SELECT COALESCE(MAX(seq),0) FROM commits WHERE seq IS NOT NULL;"; }

wait_server() {
  local url="$1" i
  for i in $(seq 1 50); do
    if curl -s -o /dev/null "$url/"; then return 0; fi
    sleep 0.2
  done
  return 1
}
start_server() {
  local addr="$1" datadir="$2" log="$3"
  mkdir -p "$datadir"
  nohup "$BIN" server start --addr "$addr" --data-dir "$datadir" >"$log" 2>&1 &
  disown 2>/dev/null || true
}
stop_server() {
  local datadir="$1"
  [ -d "$datadir" ] || return 0
  "$BIN" server stop --data-dir "$datadir" >/dev/null 2>&1 || true
}
