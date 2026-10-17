#!/usr/bin/env bash
# Unit tests for lib/tick.jq: pure input -> decisions, no Herdr involved.
set -euo pipefail
cd "$(dirname "$0")/.."

CFG='{"berthier_root":"/home/u/berthier","blocked_after":120,"long_after":3600,"task_settle":30,"revive_max":3,"revive_backoff":60,"revive_reset":300,"escalation_ttl":604800,"speak_after":30,"ask_stuck":1800,"pingpong_max":10}'
pass=0 fail=0

tick() { # prev snap now
  jq -cn --argjson prev "$1" --argjson snap "$2" --argjson now "$3" --argjson cfg "$CFG" --argjson inbox "${INBOX:-[]}" -L lib -f lib/tick.jq
}
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}

ws() { jq -cn --arg id "$1" --arg l "$2" --arg key "${3:-}" --arg path "${4:-}" \
  '{workspace_id: $id, label: $l} + (if $key == "" then {} else
     {worktree: {repo_key: $key, repo_root: ($key | rtrimstr("/.git")), checkout_path: $path}} end)'; }
agent() { jq -cn --arg p "$1" --arg s "$2" --arg sid "${3:-}" --arg name "${4:-}" --arg cwd "${5:-/x}" \
  '{pane_id: $p, workspace_id: ($p | split(":")[0]), tab_id: (($p | split(":")[0]) + ":t1"),
    agent: "claude", agent_status: $s, cwd: $cwd, terminal_title_stripped: "t",
    name: (if $name == "" then null else $name end),
    agent_session: (if $sid == "" then null else {value: $sid} end)}'; }
pane() { jq -cn --arg p "$1" '{pane_id: $p}'; }
snap() { # workspaces-json-array agents-json-array panes-json-array
  jq -cn --argjson w "$1" --argjson a "$2" --argjson p "$3" '{workspaces: $w, agents: $a, panes: $p}'; }

W="[$(ws w1 skipli /r/skipli/.git /r/skipli),$(ws w2 '󱎦 skipli/dark-mode' /r/skipli/.git /h/dark-mode),$(ws w3 berthier),$(ws w4 notes)]"

# --- First observation records agents, no actions.
s1=$(snap "$W" "[$(agent w1:p1 idle sid-1)]" "[$(pane w1:p1)]")
o=$(tick '{}' "$s1" 1000)
check "first run status" "$(jq -c '.state.agents["w1:p1"] | [.status, .status_since, .session_id]' <<<"$o")" '["idle",1000,"sid-1"]'
check "first run no actions" "$(jq -c '[.revive, .escalate, .notify] | map(length)' <<<"$o")" '[0,0,0]'

# --- Vanished agent whose pane survived: gone + revive.
prev=$(jq -c .state <<<"$o")
s2=$(snap "$W" '[]' "[$(pane w1:p1)]")
o=$(tick "$prev" "$s2" 1010)
check "gone status" "$(jq -c '.state.agents["w1:p1"] | [.status, .status_since]' <<<"$o")" '["gone",1010]'
check "revive action" "$(jq -c '.revive' <<<"$o")" '[{"pane_id":"w1:p1","name":"a-w1-p1","session_id":"sid-1"}]'
check "vanished this tick" "$(jq -c '.vanished | map(.pane_id)' <<<"$o")" '["w1:p1"]'

# --- Backoff: a recent attempt suppresses another.
prev=$(jq -c '.state | .agents["w1:p1"].revive_attempts = 1 | .agents["w1:p1"].last_revive_at = 1010' <<<"$o")
o=$(tick "$prev" "$s2" 1030)
check "revive backoff" "$(jq -c '.revive | length' <<<"$o")" '0'
check "vanished only once" "$(jq -c '.vanished | length' <<<"$o")" '0'
o=$(tick "$prev" "$s2" 1071)
check "revive after backoff" "$(jq -c '.revive | length' <<<"$o")" '1'

# --- Max attempts: escalate once, never revive again.
prev=$(jq -c '.state | .agents["w1:p1"].revive_attempts = 3 | .agents["w1:p1"].last_revive_at = 1000' <<<"$o")
o=$(tick "$prev" "$s2" 2000)
check "revive exhausted" "$(jq -c '[(.revive | length), (.escalate | map(.key))]' <<<"$o")" '[0,["revive_failed:w1:p1:1010"]]'
check "revive_failed speech" "$(jq -c '.speak | map(.text)' <<<"$o")" '["skipli crashed and could not be resumed"]'
o=$(tick "$(jq -c .state <<<"$o")" "$s2" 2010)
check "revive_failed spoken once" "$(jq -c '.speak' <<<"$o")" '[]'
check "revive_failed escalates once" "$(jq -c '.escalate | length' <<<"$o")" '0'

# --- Closed pane or no session: forgotten.
o=$(tick "$prev" "$(snap "$W" '[]' '[]')" 2000)
check "closed pane forgotten" "$(jq -c '.state.agents | length' <<<"$o")" '0'
prev=$(jq -c '.state' <<<"$(tick '{}' "$(snap "$W" "[$(agent w1:p1 idle)]" "[$(pane w1:p1)]")" 1000)")
o=$(tick "$prev" "$s2" 1010)
check "no session forgotten" "$(jq -c '.state.agents | length' <<<"$o")" '0'

# --- Blocked: escalate after threshold; Berthier's own goes to notify.
sb=$(snap "$W" "[$(agent w1:p1 blocked sid-1),$(agent w3:p1 blocked sid-b "" /home/u/berthier)]" "[$(pane w1:p1),$(pane w3:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sb" 1000)")
o=$(tick "$prev" "$sb" 1100)
check "blocked under threshold" "$(jq -c '[(.escalate | length), (.notify | length)]' <<<"$o")" '[0,0]'
o=$(tick "$prev" "$sb" 1130)
check "blocked escalates" "$(jq -c '.escalate | map(.key)' <<<"$o")" '["blocked:w1:p1:1000"]'
check "berthier blocked notifies" "$(jq -c '.notify | map(.key)' <<<"$o")" '["blocked:w3:p1:1000"]'
check "blocked speaks once" "$(jq -c '.speak' <<<"$(tick "$(jq -c .state <<<"$o")" "$sb" 1140)")" '[]'
check "blocked speech" "$(jq -c '.speak | map(.text)' <<<"$(tick "$prev" "$sb" 1030)")" '["skipli is waiting on you","berthier is waiting on you"]'
st=$(snap "$W" "[$(agent w2:p1 blocked sid-t)]" "[$(pane w2:p1)]")
check "task speech" "$(jq -c '.speak | map(.text)' <<<"$(tick "$(jq -c .state <<<"$(tick '{}' "$st" 1000)")" "$st" 1030)")" '["skipli task dark mode is waiting on you"]'
check "no speech under threshold" "$(jq -c '.speak' <<<"$(tick "$prev" "$sb" 1020)")" '[]'
check "berthier recorded" "$(jq -r '.state.berthier' <<<"$o")" 'w3:p1'

# --- Asks: oldest first, one per idle Agent per tick; a busy one waits.
sa=$(snap "$W" "[$(agent w1:p1 idle sid-1),$(agent w4:p1 working sid-4),$(agent w3:p1 idle sid-b "" /home/u/berthier)]" "[$(pane w1:p1),$(pane w4:p1),$(pane w3:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sa" 1000)")
INBOX='[{"id":"2","from":"notes","to":"Skipli","text":"again","at":950},{"id":"1","from":"notes","to":"skipli","text":"hi","at":900},{"id":"3","from":"skipli","to":"notes","text":"x","at":100},{"id":"4","from":"notes","to":"berthier","text":"who owns dns?","at":990}]'
o=$(INBOX=$INBOX tick "$prev" "$sa" 2000)
check "ask delivery" "$(jq -c '.deliver | map([.id, .pane_id])' <<<"$o")" '[["1","w1:p1"],["4","w3:p1"]]'
check "ask text" "$(jq -r '.deliver[0].text' <<<"$o")" '[berthier ask] from notes: hi (Reply or report back with: berthier ask notes "...")'
check "ask stuck escalates" "$(jq -c '.escalate | map(.key)' <<<"$o")" '["ask_stuck:3"]'
check "ask logged" "$(jq -c '.state.ask_log | map(.to)' <<<"$o")" '["skipli","berthier"]'
oneway=$(jq -c '[range(12) | {from: "notes", to: "skipli", at: (1900 + .)}]' <<<null)
o=$(INBOX='[]' tick "$(jq -c --argjson l "$oneway" '.ask_log = $l' <<<"$prev")" "$sa" 2000)
check "one-way updates are not pingpong" "$(jq -c '.escalate | map(.key)' <<<"$o")" '[]'
flips=$(jq -c '[range(11) | {from: (if . % 2 == 0 then "skipli" else "notes" end), to: (if . % 2 == 0 then "notes" else "skipli" end), at: (1900 + .)}]' <<<null)
o=$(INBOX='[]' tick "$(jq -c --argjson l "$flips" '.ask_log = $l' <<<"$prev")" "$sa" 2000)
check "pingpong escalates" "$(jq -c '.escalate | map(.text)' <<<"$o")" '["asks bouncing between notes and skipli: 10 back-and-forths in the last hour"]'

# --- Long working.
sw=$(snap "$W" "[$(agent w1:p1 working sid-1)]" "[$(pane w1:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sw" 1000)")
check "long working" "$(jq -c '.escalate | map(.key)' <<<"$(tick "$prev" "$sw" 4600)")" '["long:w1:p1:1000"]'

# --- Task agent stops working: escalate after settle; Corps agent does not.
sw=$(snap "$W" "[$(agent w2:p1 working sid-2),$(agent w1:p1 working sid-1)]" "[$(pane w2:p1),$(pane w1:p1)]")
si=$(snap "$W" "[$(agent w2:p1 idle sid-2),$(agent w1:p1 idle sid-1)]" "[$(pane w2:p1),$(pane w1:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sw" 1000)")
prev=$(jq -c .state <<<"$(tick "$prev" "$si" 1100)")
check "task settle wait" "$(jq -c '.escalate | length' <<<"$(tick "$prev" "$si" 1110)")" '0'
check "task stopped" "$(jq -c '.escalate | map(.key)' <<<"$(tick "$prev" "$si" 1131)")" '["task_stopped:w2:p1:1100"]'

# --- Corps: Task grouped under its repo, Berthier and Tasks excluded.
o=$(tick '{}' "$si" 1000)
check "corps" "$(jq -c '.state.corps | map({name, tasks: (.tasks | map(.name))})' <<<"$o")" \
  '[{"name":"skipli","tasks":["skipli/dark-mode"]},{"name":"notes","tasks":[]}]'

# --- Berthier is whoever runs in its repo (subdirs too), whatever the workspace label.
Wh="[$(ws w1 skipli),$(ws w5 hq)]"
sh=$(snap "$Wh" "[$(agent w5:p1 idle sid-h "" /home/u/berthier/tests),$(agent w1:p1 idle sid-1 "" /home/u/berthier-other)]" "[$(pane w5:p1),$(pane w1:p1)]")
o=$(tick '{}' "$sh" 1000)
check "berthier by cwd" "$(jq -c '[.state.berthier, (.state.corps | map(.name))]' <<<"$o")" '["w5:p1",["skipli"]]'

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
