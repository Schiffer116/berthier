#!/usr/bin/env bash
# End-to-end test against an isolated Herdr session with real Claude agents
# (haiku, a few cents). Never touches the user's default Herdr session.
#   tests/e2e.sh
set -euo pipefail
cd "$(dirname "$0")/.."

session=berthier-e2e
work=$(mktemp -d)
repo=$work/berthier-e2e-$$
export BERTHIER_HERDR_SESSION=$session BERTHIER_HOME=$work/home BERTHIER_START_TIMEOUT_MS=60000
clean_env=(env -u HERDR_ENV -u HERDR_SOCKET_PATH -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID)
h() { "${clean_env[@]}" herdr --session "$session" "$@"; }
step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; exit 1; }
claude_pid() { h pane process-info --pane "$1" | jq -r '.result.process_info.foreground_processes[] | select(.name == "claude") | .pid'; }

cleanup() {
  h session stop "$session" >/dev/null 2>&1 || "${clean_env[@]}" herdr session stop "$session" >/dev/null 2>&1 || true
  "${clean_env[@]}" herdr session delete "$session" >/dev/null 2>&1 || true
  tmux kill-session -t "$session" 2>/dev/null || true
  rm -rf "$work" "$HOME/.herdr/worktrees/${repo##*/}"
}
trap cleanup EXIT

step "start isolated Herdr session"
# Agents inherit BERTHIER_HOME from Herdr, so their berthier commands use the test state.
tmux new-session -d -s "$session" -x 200 -y 50 "${clean_env[*]} BERTHIER_HOME=$BERTHIER_HOME herdr session attach $session"
for _ in $(seq 50); do h workspace list >/dev/null 2>&1 && break; sleep 0.2; done

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
grep -q "revived demo-main in $pane" "$BERTHIER_HOME/loop.log" || fail "revive not logged"
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
grep -q "delivered ask .* to $pane" "$BERTHIER_HOME/loop.log" || fail "ask not delivered"
h agent wait demo-main --until idle --timeout 90000 >/dev/null
h pane read "$pane" --source recent-unwrapped --lines 60 | grep -q '\[berthier ask\] from helper' || fail "ask not in demo-main's pane"
[[ -z $(ls "$BERTHIER_HOME/inbox") ]] || fail "delivered ask left in the inbox"

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
grep -q "forgot demo-main in $pane: user exited it" "$BERTHIER_HOME/loop.log" || fail "deliberate exit not recognized"
jq -e --arg p "$pane" '.agents[$p] == null' "$BERTHIER_HOME/state.json" >/dev/null || fail "exited agent still tracked"

step "task-split creates a 󱎦 worktree workspace; task-done removes it"
BERTHIER_WORKSPACE="$ws" \
  BERTHIER_START_TIMEOUT_MS=5000 bin/berthier task-split "Dark Mode" "Reply with just: hi"
h workspace list | jq -e 'any(.result.workspaces[]; .label == "󱎦 demo/dark-mode")' >/dev/null || fail "task workspace missing"
bin/berthier tick
jq -e 'any(.corps[].tasks[]; .name == "demo/dark-mode")' "$BERTHIER_HOME/state.json" >/dev/null || fail "state does not show the task"
BERTHIER_WORKSPACE="$ws" bin/berthier task-done "Dark Mode"
h workspace list | jq -e 'any(.result.workspaces[]; .label == "󱎦 demo/dark-mode") | not' >/dev/null || fail "task workspace not removed"
git -C "$repo" show-ref --verify --quiet refs/heads/dark-mode && fail "merged task branch not deleted"

printf '\nE2E PASSED\n'
