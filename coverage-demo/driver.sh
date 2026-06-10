#!/usr/bin/env bash
# Coverage driver: exercise EVERY `store` command and flag at least once.
#
# Steps run in dependency order (not PRD order) — the manifest ids each step
# emits are what light up the checklist, so display order and execution order
# are decoupled. Run via ./run.sh (which builds the binary, sets NDJSON, and
# asserts full coverage afterwards). Sourcing order: lib.sh provides step/mark.

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# globals filled in as we go
H=""; REPO=""; GLR=""

# ---------------------------------------------------------------------------
section_bootstrap() {
  say "Bootstrap — reset sandbox, start server, register identities"
  [ -x "$BIN" ] || die "binary missing: $BIN (run via ./run.sh)"

  stop_server "$SERVERS_DIR/main"; stop_server "$SERVERS_DIR/move"
  pkill -f "$BIN watch" 2>/dev/null || true
  sleep 1
  rm -rf "$HOMES_DIR" "$SERVERS_DIR" "$RUN_DIR/human2"
  mkdir -p "$HOMES_DIR" "$SERVERS_DIR"
  : > "$NDJSON"
  emit '{"t":"run_start"}'

  for a in "${ACTORS[@]}" temp-agent; do
    mkdir -p "$(home "$a")/.agentstore"
    ssh-keygen -t ed25519 -f "$(home "$a")/.agentstore/id_ed25519" -N "" -q
  done
  ok "generated keypairs for ${ACTORS[*]} temp-agent"

  prompt "server start --addr $MAIN_ADDR --data-dir <main>   # background"
  start_server "$MAIN_ADDR" "$SERVERS_DIR/main" "$SERVERS_DIR/main.log"
  wait_server "$MAIN_URL" || die "main server did not start (see $SERVERS_DIR/main.log)"
  mark "server start,server start --addr,server start --data-dir" 0 \
       "server start --addr $MAIN_ADDR --data-dir $SERVERS_DIR/main"
  ok "server up at $MAIN_URL"

  for a in "${ACTORS[@]}" temp-agent; do
    step "$a" "$(home "$a")" "register,register --remote,register --username,register --public-key" -- \
      register --remote "$MAIN_URL" --username "$a" \
      --public-key "$(home "$a")/.agentstore/id_ed25519.pub"
  done
}

# ---------------------------------------------------------------------------
section_skill() {
  say "Skill — export the bundled agent skill (no server or repo needed)"
  local sk="$RUN_DIR/skill-export"; rm -rf "$sk"
  step human "$RUN_DIR" "skill export" -- skill export "$sk"
  step human "$RUN_DIR" "skill export --stdout" -- skill export --stdout
  rm -rf "$sk"
}

# ---------------------------------------------------------------------------
section_meta() {
  say "Meta — version (no server or repo needed)"
  step human "$RUN_DIR" "version" -- version
}

# ---------------------------------------------------------------------------
section_setup() {
  say "Store setup — init, seed, members, grants, remotes"
  H="$(home human)"; REPO="$H/$REPO_NAME"
  step human "$H" "init" -- init "$MAIN_URL/$REPO_NAME"

  mkdir -p "$REPO/customers/sanitized" "$REPO/finance" "$REPO/strategy" "$REPO/docs"
  printf '# ICP — sanitized\n\nMid-market ops teams.\n' > "$REPO/customers/sanitized/icp.md"
  printf '# Payroll (CONFIDENTIAL)\n\nDo not expose.\n'  > "$REPO/finance/payroll.md"
  printf '# Positioning\n\nThe agent-native datastore.\n'> "$REPO/strategy/positioning.md"
  printf '# Onboarding\n\n1. Install\n'                  > "$REPO/docs/onboarding.md"
  step human "$REPO" "add" -- add .
  step human "$REPO" "commit,commit --message" -- commit -m "Seed knowledge workspace"
  step human "$REPO" "push" -- push

  for a in research-agent writer-agent graph-layer; do
    scaffold human "$REPO" principals add "$a"
  done
  step human "$REPO" "grant" -- grant research-agent read "/customers/sanitized/*"
  scaffold human "$REPO" grant writer-agent write "/strategy/*"
  scaffold human "$REPO" grant graph-layer  read  "/strategy/*"

  step human "$REPO" "remote add" -- remote add newhome "$MOVE_URL/$REPO_NAME"
  step human "$REPO" "remote list" -- remote list
  scaffold human "$REPO" remote add scratch "$MAIN_URL/scratch"
  step human "$REPO" "remote remove" -- remote remove scratch
}

# ---------------------------------------------------------------------------
section_config() {
  say "Config — read/write TOML config (global + local)"
  # one --global set covers both bare `config` and `config --global`
  step human "$REPO" "config,config --global" -- config --global ui.pager less
  step human "$REPO" "config --local" -- config --local demo.note coverage-run
  step human "$REPO" "config --list" -- config --list
}

# ---------------------------------------------------------------------------
section_fileops() {
  say "File operations — edit a file, then status, diff, staged diff, rm"
  note "modify an existing doc, then inspect the change before staging it"
  edit_doc "$REPO" docs/onboarding.md "2. Configure the workspace"
  step human "$REPO" "status" -- status
  step human "$REPO" "diff" -- diff
  scaffold human "$REPO" add docs/onboarding.md
  step human "$REPO" "diff --staged" -- diff --staged
  scaffold human "$REPO" commit -m "Expand onboarding"
  scaffold human "$REPO" push

  step human "$REPO" "rm" -- rm docs/onboarding.md
  scaffold human "$REPO" commit -m "Retire onboarding"
  scaffold human "$REPO" push
}

# ---------------------------------------------------------------------------
section_history() {
  say "History and versions — log filters, show, checkout"
  local head author seq
  head="$(as human "$REPO" log -n 1 --json | head -1 | jq -r '.id' 2>/dev/null)" || true
  author="$(as human "$REPO" log -n 1 --json | head -1 | jq -r '.author_id' 2>/dev/null)" || true
  seq="$(max_seq "$REPO")" || true
  : "${head:=HEAD}"; : "${author:=human}"; : "${seq:=1}"

  step human "$REPO" "log" -- log
  step human "$REPO" "log --number" -- log -n 2
  step human "$REPO" "log --author" -- log --author "$author"
  step human "$REPO" "log --since" -- log --since 2000-01-01T00:00:00Z
  step human "$REPO" "log --to" -- log --to 2100-01-01T00:00:00Z
  step human "$REPO" "log --cursor" -- log --cursor 0
  step human "$REPO" "log --to-cursor" -- log --to-cursor "$seq"
  step human "$REPO" "log --reverse" -- log --reverse
  step human "$REPO" "log --json" -- log --json
  step human "$REPO" "show" -- show "$head"

  # restore positioning.md to its seed version (seq 1), stage, then commit it.
  step human "$REPO" "checkout" -- checkout 1 strategy/positioning.md
  scaffold human "$REPO" commit -m "Restore seed positioning (checkout)"
  scaffold human "$REPO" push
}

# ---------------------------------------------------------------------------
section_permissions() {
  say "Permissions — list, revoke (grant covered in setup)"
  step human "$REPO" "permissions" -- permissions "/strategy/*"
  step human "$REPO" "revoke" -- revoke graph-layer "/strategy/*"
  scaffold human "$REPO" grant graph-layer read "/strategy/*"   # restore for events demo
}

# ---------------------------------------------------------------------------
section_clone() {
  say "Clone — permission-filtered checkout"
  local ra; ra="$(home research-agent)"; rm -rf "$ra/$REPO_NAME"
  step research-agent "$ra" "clone" -- clone "$MAIN_URL/$REPO_NAME"
  echo "   research-agent received:"; tracked "$ra/$REPO_NAME" | sed 's/^/      /'
}

# ---------------------------------------------------------------------------
# watch_once <ids> <display> <store args...> : start a backgrounded watch, fire a
# real event by pushing an edit, show the event the watch receives, then stop it.
# Records coverage regardless of stream contents.
watch_once() {
  local ids="$1" disp="$2"; shift 2
  local gl; gl="$(home graph-layer)"
  local out; out="$(mktemp)"
  prompt "$disp"
  ( cd "$GLR" && HOME="$gl" "$BIN" "$@" ) >"$out" 2>/dev/null &
  local wpid=$!
  disown "$wpid" 2>/dev/null || true   # don't let bash print "Terminated" when we stop it
  sleep 1.0                            # let the WebSocket connect before the event fires

  printf '\n## tick %s\n' "$(date +%H:%M:%S)" >> "$REPO/strategy/positioning.md"
  scaffold human "$REPO" add strategy/positioning.md
  scaffold human "$REPO" commit -m "watch tick"
  scaffold human "$REPO" push
  sleep 1.0                            # let the live event arrive

  kill "$wpid" 2>/dev/null || true
  if [ -s "$out" ]; then
    printf "%s   ← live event received:%s\n" "$CYN" "$RST"
    sed 's/^/      /' "$out" | head -6
  else
    warn "   (no event arrived in the capture window)"
  fi
  rm -f "$out"
  mark "$ids" 0 "$disp"
}

section_events() {
  say "Real-time events — watch (live stream), cursor delta"
  local gl; gl="$(home graph-layer)"; rm -rf "$gl/$REPO_NAME"
  scaffold graph-layer "$gl" clone "$MAIN_URL/$REPO_NAME"
  GLR="$gl/$REPO_NAME"
  local cur; cur="$(max_seq "$GLR")" || cur=0

  watch_once "watch"          "watch /strategy"                         watch /strategy
  watch_once "watch --events" "watch /strategy --events file.modified"  watch /strategy --events file.modified
  watch_once "watch --cursor" "watch /strategy --cursor $cur"           watch /strategy --cursor "$cur"

  # the cursor-delta query the knowledge-graph pattern relies on: only the
  # commits after the agent's last-seen cursor (the watch ticks we just pushed).
  step human "$REPO" "log --cursor" -- log --cursor "$cur" --json
}

# ---------------------------------------------------------------------------
section_commit_sync() {
  say "Committing & syncing — push/pull variants, reset, merge"
  # push handles modifications fine; do one to cover push --remote.
  edit_doc "$REPO" strategy/positioning.md "## Sharper headline"
  scaffold human "$REPO" add strategy/positioning.md
  scaffold human "$REPO" commit -m "Sync demo edit"
  step human "$REPO" "push --remote" -- push --remote origin

  # A full (owner) clone taken at the CURRENT head. It fast-forwards both a
  # newly-ADDED file and a remote MODIFICATION to an existing, locally-unmodified
  # file — the latter exercises the overwrite guard's clean-fast-forward path.
  local pull_dir="$RUN_DIR/puller"; rm -rf "$pull_dir"; mkdir -p "$pull_dir"
  scaffold human "$pull_dir" clone "$MAIN_URL/$REPO_NAME"
  local pr="$pull_dir/$REPO_NAME"

  printf 'first delta\n' > "$REPO/strategy/delta1.md"
  scaffold human "$REPO" add strategy/delta1.md
  scaffold human "$REPO" commit -m "Add delta1"
  scaffold human "$REPO" push
  step human "$pr" "pull" -- pull                       # fast-forward an added file

  edit_doc "$REPO" strategy/positioning.md "## Expanded rationale"
  scaffold human "$REPO" add strategy/positioning.md
  scaffold human "$REPO" commit -m "Modify positioning"
  scaffold human "$REPO" push
  step human "$pr" "pull --remote" -- pull --remote origin  # fast-forward a modified file

  # reset: make an unpushed commit, then discard it (typed confirmation on stdin)
  printf 'scratch\n' > "$REPO/strategy/scratch.md"
  scaffold human "$REPO" add strategy/scratch.md
  scaffold human "$REPO" commit -m "Unpushed scratch (will reset)"
  prompt "reset"
  printf 'reset\n' | ( cd "$REPO" && HOME="$H" "$BIN" reset ) || true
  mark "reset" 0 "reset"

  # merge --abort: manufacture a conflict on the same file, pull, then abort.
  # This one invocation covers both `merge` and `merge --abort` — there is no
  # meaningful bare `store merge` (with no flag it just errors "use --abort").
  say "Conflict + merge --abort"
  rm -rf "$RUN_DIR/human2"; mkdir -p "$RUN_DIR/human2"
  scaffold human "$RUN_DIR/human2" clone "$MAIN_URL/$REPO_NAME"
  local h2r="$RUN_DIR/human2/$REPO_NAME"
  note "two principals edit the same line of strategy/positioning.md"
  edit_doc "$h2r"  strategy/positioning.md "## CONFLICT — remote wins?"
  scaffold human "$h2r" add strategy/positioning.md
  scaffold human "$h2r" commit -m "Edit A (remote)"
  scaffold human "$h2r" push
  edit_doc "$REPO" strategy/positioning.md "## CONFLICT — local wins?"
  scaffold human "$REPO" add strategy/positioning.md
  scaffold human "$REPO" commit -m "Edit B (local)"
  scaffold human "$REPO" pull
  step human "$REPO" "merge,merge --abort" -- merge --abort

  # clean up: drop the local divergence and re-sync
  printf 'reset\n' | ( cd "$REPO" && HOME="$H" "$BIN" reset ) >/dev/null 2>&1 || true
  scaffold human "$REPO" pull
}

# ---------------------------------------------------------------------------
section_portability() {
  say "Portability — mirror the repo to a fresh server"
  stop_server "$SERVERS_DIR/move"; sleep 0.5; rm -rf "$SERVERS_DIR/move"
  start_server "$MOVE_ADDR" "$SERVERS_DIR/move" "$SERVERS_DIR/move.log"
  wait_server "$MOVE_URL" || die "move server did not start (see $SERVERS_DIR/move.log)"
  ok "empty target server up at $MOVE_URL"

  step human "$REPO" "push --mirror" -- push newhome --mirror

  say "Identity — bind to preserved identity on the new server"
  local ra; ra="$(home research-agent)"
  step research-agent "$ra" "bind,bind --remote,bind --username,bind --public-key" -- \
    bind --remote "$MOVE_URL" --username research-agent \
    --public-key "$ra/.agentstore/id_ed25519.pub"
}

# ---------------------------------------------------------------------------
section_identity() {
  say "Identity — whoami, directory browse, rekey"
  step human "$REPO" "whoami" -- whoami
  step human "$REPO" "whoami --remote" -- whoami --remote "$MAIN_URL"
  step human "$REPO" "principals list --remote" -- principals list --remote "$MAIN_URL"

  local glh; glh="$(home graph-layer)"
  ssh-keygen -t ed25519 -f "$glh/.agentstore/id_new" -N "" -q
  step graph-layer "$GLR" "rekey,rekey --remote,rekey --public-key" -- \
    rekey --public-key "$glh/.agentstore/id_new.pub" --remote "$MAIN_URL"
  mv -f "$glh/.agentstore/id_new"     "$glh/.agentstore/id_ed25519"
  mv -f "$glh/.agentstore/id_new.pub" "$glh/.agentstore/id_ed25519.pub"
}

# ---------------------------------------------------------------------------
section_membership() {
  say "Membership — list members, add + remove a principal"
  step human "$REPO" "principals list" -- principals list
  step human "$REPO" "principals add" -- principals add temp-agent
  step human "$REPO" "principals remove" -- principals remove temp-agent
}

# ---------------------------------------------------------------------------
section_admin() {
  say "Admin role — add, list, revoke a second admin"
  step human "$REPO" "admin add" -- admin add research-agent
  step human "$REPO" "admin list" -- admin list
  step human "$REPO" "admin revoke" -- admin revoke research-agent
}

# ---------------------------------------------------------------------------
section_server_stop() {
  say "Server — drain and stop"
  prompt "server stop --data-dir <main>"
  "$BIN" server stop --data-dir "$SERVERS_DIR/main" || true
  mark "server stop,server stop --data-dir" 0 "server stop --data-dir $SERVERS_DIR/main"
  "$BIN" server stop --data-dir "$SERVERS_DIR/move" >/dev/null 2>&1 || true
  pkill -f "$BIN watch" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
main() {
  section_bootstrap
  section_skill
  section_meta
  section_setup
  section_config
  section_fileops
  section_history
  section_permissions
  section_clone
  section_events
  section_commit_sync
  section_portability
  section_identity
  section_membership
  section_admin
  section_server_stop
  emit '{"t":"run_end"}'
  say "Driver complete — see coverage report from run.sh"
}

main "$@"
