#!/usr/bin/env bash
# End-to-end test against an isolated Herdr session with real Claude agents
# (haiku, a few cents). Never touches the user's default Herdr session.
#   tests/e2e.sh
set -euo pipefail
cd "$(dirname "$0")/.."

session=berthier-e2e-$$
work=$(mktemp -d)
repo=$work/berthier-e2e-$$
export BERTHIER_HERDR_SESSION=$session BERTHIER_HOME=$work/home BERTHIER_START_TIMEOUT_MS=60000
clean_env=(env -u HERDR_ENV -u HERDR_SOCKET_PATH -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID)
h() { "${clean_env[@]}" herdr --session "$session" "$@"; }
step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; exit 1; }
# How many loop.log events match a jq condition, e.g. logged '.kind == "revived"'.
logged() { jq -c --arg p "${2:-}" "select($1)" "${3:-$BERTHIER_HOME/loop.log}" | wc -l; }
claude_pid() { h pane process-info --pane "$1" | jq -r '.result.process_info.foreground_processes[] | select(.name == "claude") | .pid'; }

cleanup() {
  h session stop "$session" >/dev/null 2>&1 || "${clean_env[@]}" herdr session stop "$session" >/dev/null 2>&1 || true
  "${clean_env[@]}" herdr session delete "$session" >/dev/null 2>&1 || true
  tmux kill-session -t "$session" 2>/dev/null || true
  rm -rf "$work" "$HOME/.herdr/worktrees/${repo##*/}"
}
trap cleanup EXIT

attach() {
  # Agents inherit BERTHIER_HOME from Herdr, so their berthier commands use the test state.
  tmux new-session -d -s "$session" -x 200 -y 50 "${clean_env[*]} BERTHIER_HOME=$BERTHIER_HOME herdr session attach $session"
  for _ in $(seq 50); do h workspace list >/dev/null 2>&1 && break; sleep 0.2; done
}

step "start isolated Herdr session"
attach

mkdir -p "$repo" && git -C "$repo" init -q && git -C "$repo" commit -q --allow-empty -m init
pane=$(h workspace create --cwd "$repo" --label demo --no-focus | jq -re '.result.root_pane.pane_id')
ws=$(h pane get "$pane" | jq -re '.result.pane.workspace_id')

step "start a Main Agent with one turn (so its session can be resumed)"
# The scratch dir is new to Claude: accept its folder trust dialog for this test.
h agent start demo-main --kind claude --pane "$pane" --timeout 60000 -- --model haiku >/dev/null 2>&1 || {
  h agent send-keys demo-main down enter >/dev/null
  h agent wait demo-main --until idle --timeout 60000 >/dev/null
}
# Right after the trust dialog Claude can report idle before it accepts input.
for try in 1 2 3; do
  h agent prompt demo-main "Reply with just: pong" --wait --timeout 90000 >/dev/null 2>&1 && break
  ((try < 3)) || fail "agent never took its first prompt"
  sleep 3
done
bin/berthier tick
sid=$(jq -r --arg p "$pane" '.agents[$p].session_id' "$BERTHIER_HOME/state.json")
[[ $sid =~ ^[0-9a-f-]{36}$ ]] || fail "no session id recorded (got $sid)"

step "crash the agent; the Main Loop must Revive it in place"
kill -KILL "$(claude_pid "$pane")"
sleep 3
bin/berthier tick
for _ in $(seq 30); do
  [[ $(h agent get "$pane" 2>/dev/null | jq -r '.result.agent.agent_session.value // empty') == "$sid" ]] && break
  sleep 1
done
[[ $(h agent get "$pane" | jq -r '.result.agent.agent_session.value') == "$sid" ]] || fail "agent not revived with session $sid"
(($(logged '.kind == "revived" and .pane == $p and .name == "demo-main"' "$pane"))) || fail "revive not logged"
bin/berthier tick  # the loop's next pass sees the revived agent live

step "the Stop hook makes an Agent report; an ask reaches an idle Agent"
helper=$(h workspace create --cwd "$repo" --label helper --no-focus | jq -re '.result.root_pane.pane_id')
hook=$(jq -cn --arg c "$PWD/bin/berthier hook-stop" '{hooks: {Stop: [{hooks: [{type: "command", command: $c}]}]}}')
h agent start helper --kind claude --pane "$helper" --timeout 60000 -- --model haiku --settings "$hook" >/dev/null 2>&1
h agent prompt helper "Reply with just: pong" --wait --timeout 120000 >/dev/null
helper_sid=$(h agent get helper | jq -r '.result.agent.agent_session.value')
[[ -s $BERTHIER_HOME/reports/$helper_sid.json ]] || fail "helper ended its turn without a report"
echo "helper reported: $(jq -r .line "$BERTHIER_HOME/reports/$helper_sid.json")"
bin/berthier tick
HERDR_PANE_ID=$helper bin/berthier ask demo "Reply with just: ack" >/dev/null
h agent wait demo-main --until idle --timeout 60000 >/dev/null
bin/berthier tick
(($(logged '.kind == "delivered" and .pane == $p and .from == "helper" and .to == "demo"' "$pane"))) || fail "ask not delivered"
h agent wait demo-main --timeout 90000 >/dev/null
h pane read "$pane" --source recent-unwrapped --lines 60 | grep -q '\[berthier ask\] from helper' || fail "ask not in demo-main's pane"
# The Agent may answer with an ask of its own, to helper.
grep -qsF '"to":"demo"' "$BERTHIER_HOME"/inbox/*.json && fail "delivered ask left in the inbox"

step "an ask naming oversized screenshots is submitted, not left as a draft"
# Claude resizes big images while it pastes their paths and drops the Enter
# that arrives meanwhile (2026-10-21, skipli).
python3 - "$work" <<'PY'
import struct, sys, zlib
w = h = 2400
raw = b"".join(b"\0" + bytes((x + y) % 256 for x in range(w * 3)) for y in range(h))
chunk = lambda t, d: struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) \
    + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
for i in range(4):
    open(f"{sys.argv[1]}/shot{i}.png", "wb").write(png)
PY
mark=$(wc -l <"$BERTHIER_HOME/loop.log")
HERDR_PANE_ID=$helper bin/berthier ask demo "Screenshots: $work/shot0.png $work/shot1.png $work/shot2.png $work/shot3.png. Reply with just: seen" >/dev/null
bin/berthier tick
tail -n "+$((mark + 1))" "$BERTHIER_HOME/loop.log" >"$work/since"
(($(logged '.kind == "delivered" and .pane == $p and .from == "helper" and .to == "demo"' "$pane" "$work/since"))) \
  || fail "image ask not delivered: $(cat "$work/since")"
h agent wait demo-main --timeout 90000 >/dev/null
# Claude lists a sent prompt's images under it; a draft only shows [Image #n].
h pane read "$pane" --source recent-unwrapped --lines 60 | grep -q '⎿.*\[Image #' \
  || fail "image ask not submitted: $(h pane read "$pane" --source recent-unwrapped --lines 60)"

step "adopt makes helper a Division of demo"
bin/berthier adopt helper demo >/dev/null
bin/berthier tick
jq -e '.corps[] | select(.name == "demo") | .divisions | map(.name) == ["helper"]' "$BERTHIER_HOME/state.json" >/dev/null \
  || fail "helper not nested under demo"

step "a Herdr restart keeps every name and Revives nothing"
# Herdr resumes Claude sessions itself after a restart, but forgets names it did
# not assign at agent start (e.g. agent rename) and briefly lists those panes
# without an agent.
typed=$(h workspace create --cwd "$repo" --label typed --no-focus | jq -re '.result.root_pane.pane_id')
h pane run "$typed" "claude --model haiku" >/dev/null
for _ in $(seq 30); do h agent get "$typed" >/dev/null 2>&1 && break; sleep 1; done
h agent wait "$typed" --until idle --timeout 60000 >/dev/null
h agent prompt "$typed" "Reply with just: pong" --wait --timeout 90000 >/dev/null
h agent rename "$typed" typed >/dev/null
bin/berthier tick
names() { h api snapshot | jq -c '[.result.snapshot.agents[] | [.pane_id, .name, .agent_session.value]] | sort'; }
before=$(names) mark=$(wc -l <"$BERTHIER_HOME/loop.log")
h server stop >/dev/null
tmux kill-session -t "$session" 2>/dev/null || true
attach
for _ in $(seq 20); do bin/berthier tick; sleep 1; done
[[ $(names) == "$before" ]] || fail "names or sessions changed across restart: $before -> $(names)"
since=$work/since.log
tail -n "+$((mark + 1))" "$BERTHIER_HOME/loop.log" >"$since"
(($(logged '.kind | startswith("revive")' "" "$since"))) && fail "Revive attempted after restart: $(cat "$since")"
(($(logged '.kind == "renamed" and .pane == $p and .name == "typed"' "$typed" "$since"))) || fail "name restore not logged"

step "an Agent stopped on its usage limit is nudged once the limit has reset"
# Haiku plays the stop (Claude renders the line as a real one); without hooks,
# so the Stop hook's report does not become its last output.
limited=$(h workspace create --cwd "$repo" --label limited --no-focus | jq -re '.result.root_pane.pane_id')
h pane run "$limited" "claude --model haiku --settings '{\"disableAllHooks\": true}'" >/dev/null
for _ in $(seq 30); do h agent get "$limited" >/dev/null 2>&1 && break; sleep 1; done
h agent wait "$limited" --until idle --timeout 60000 >/dev/null
h agent rename "$limited" limited >/dev/null
bin/berthier tick
# More history than the screen holds, on screen, so a read that scrolls shows.
h agent prompt limited "Print the numbers 1 to 80, one per line, nothing else." --wait --timeout 90000 >/dev/null
h workspace focus "$(h pane get "$limited" | jq -re '.result.pane.workspace_id')" >/dev/null
sleep 1
# The tick reads the newly idle pane for a limit stop; the user's view must not move.
screen() { tmux capture-pane -p -t "$session" | sed 's/^[^│]*│//'; }
still=$(screen | md5sum) frames=$work/frames
(for _ in $(seq 150); do screen | md5sum; sleep 0.01; done >"$frames") &
bin/berthier tick
wait
grep -qv "$still" "$frames" && fail "a tick moved the idle pane: $(sort "$frames" | uniq -c)"
clock=$(TZ=UTC date -d '-5 min' '+%-I:%M%P')
h agent prompt limited "Reply with exactly this line and nothing else: You've hit your session limit · resets $clock (UTC)" \
  --wait --timeout 90000 >/dev/null
bin/berthier tick
(($(logged '.kind == "limit_stop" and .pane == $p and .name == "limited"' "$limited"))) \
  || fail "usage-limit stop not detected: $(h pane read "$limited" --source recent-unwrapped --lines 200 | grep -v "^\s*$")"
bin/berthier tick
(($(logged '.kind == "resumed" and .pane == $p' "$limited"))) || fail "limited agent not resumed: $(tail -5 "$BERTHIER_HOME/loop.log"; jq -c --arg p "$limited" ".agents[\$p]" "$BERTHIER_HOME/state.json")"
for _ in $(seq 30); do
  h pane read "$limited" --source recent-unwrapped --lines 200 | grep -qF "The usage limit has reset. Continue exactly" && break
  sleep 1
done
h pane read "$limited" --source recent-unwrapped --lines 200 | grep -qF "The usage limit has reset. Continue exactly" \
  || fail "resume prompt not in the pane"
bin/berthier tick
bin/berthier tick
(($(logged '.kind == "resumed" and .pane == $p' "$limited") == 1)) || fail "resumed more than once"

step "a deliberate /exit must not be Revived"
h agent wait "$pane" --timeout 60000 >/dev/null
h pane send-text "$pane" "/exit" >/dev/null
sleep 0.3
h pane send-keys "$pane" enter >/dev/null
for _ in $(seq 20); do h agent get "$pane" >/dev/null 2>&1 || break; sleep 0.5; done
sleep 1
bin/berthier tick
sleep 3
h agent get "$pane" >/dev/null 2>&1 && fail "deliberately exited agent was revived"
(($(logged '.kind == "forgot" and .pane == $p and .name == "demo-main"' "$pane"))) || fail "deliberate exit not recognized"
jq -e --arg p "$pane" '.agents[$p] == null' "$BERTHIER_HOME/state.json" >/dev/null || fail "exited agent still tracked"

step "division-new spawns a worktree Division; division-done removes it"
# Herdr refuses a multi-line Brief as a start argument; it goes as a prompt.
out=$(BERTHIER_WORKSPACE="$ws" bin/berthier division-new "Dark Mode" $'Reply with just: first\nThen, on its own line: second')
echo "$out"
[[ $out == *" started: "* ]] || fail "division-new misreported its start"
h agent get demo-dark-mode >/dev/null 2>&1 || fail "division agent left unnamed"
h agent wait demo-dark-mode --timeout 90000 >/dev/null
h agent read demo-dark-mode --source recent-unwrapped --lines 40 | grep -q '^  second' || fail "multi-line Brief not answered"
h workspace list | jq -e 'any(.result.workspaces[]; .label == "dark-mode")' >/dev/null || fail "division workspace missing"
bin/berthier tick
jq -e '.corps[] | select(.name == "demo") | any(.divisions[]; .name == "dark-mode" and .worktree)' "$BERTHIER_HOME/state.json" >/dev/null \
  || fail "state does not show the division under demo"
BERTHIER_WORKSPACE="$ws" bin/berthier division-done "Dark Mode"
h workspace list | jq -e 'any(.result.workspaces[]; .label == "dark-mode") | not' >/dev/null || fail "division workspace not removed"
git -C "$repo" show-ref --verify --quiet refs/heads/dark-mode && fail "merged division branch not deleted"
jq -e '.["dark-mode"] == null' "$BERTHIER_HOME/divisions.json" >/dev/null || fail "division still recorded"

printf '\nE2E PASSED\n'
