#!/usr/bin/env bash
# Unit tests for lib/tick.jq: pure input -> decisions, no Herdr involved.
set -euo pipefail
cd "$(dirname "$0")/.."

CFG='{"berthier_root":"/home/u/berthier","blocked_after":120,"long_after":3600,"division_settle":30,"revive_max":3,"revive_backoff":60,"revive_reset":300,"escalation_ttl":604800,"speak_after":30,"ask_stuck":1800,"pingpong_max":10,"limit_grace":60,"limit_retry":1800}'
pass=0 fail=0

tick() { # prev snap now
  jq -cn --argjson prev "$1" --argjson snap "$2" --argjson now "$3" --argjson cfg "$CFG" --argjson inbox "${INBOX:-[]}" --argjson divisions "${DIVISIONS:-{\}}" \
    ${OBS:+--argjson obs "$OBS"} -L lib -f lib/tick.jq
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

W="[$(ws w1 skipli /r/skipli/.git /r/skipli),$(ws w2 dark-mode /r/skipli/.git /h/dark-mode),$(ws w3 berthier),$(ws w4 notes)]"
D0='{"dark-mode":{"corps":"skipli","worktree":true}}'
DIVISIONS=$D0

# --- First observation records agents, no actions.
s1=$(snap "$W" "[$(agent w1:p1 idle sid-1)]" "[$(pane w1:p1)]")
o=$(tick '{}' "$s1" 1000)
check "first run status" "$(jq -c '.state.agents["w1:p1"] | [.status, .status_since, .session_id]' <<<"$o")" '["idle",1000,"sid-1"]'
check "first run no actions" "$(jq -c '[.revive, .escalate, .notify] | map(length)' <<<"$o")" '[0,0,0]'

# --- Vanished agent whose pane survived: read its pane, then gone + revive.
prev=$(jq -c .state <<<"$o")
s2=$(snap "$W" '[]' "[$(pane w1:p1)]")
o=$(tick "$prev" "$s2" 1010)
check "vanished is looked at first" "$(jq -c '[.look, .revive]' <<<"$o")" '[[{"pane_id":"w1:p1","session_id":"sid-1","want":["exited","at_shell"]}],[]]'
check "gone status" "$(jq -c '.state.agents["w1:p1"] | [.status, .status_since, .revive_attempts]' <<<"$o")" '["gone",1010,0]'
crashed='{"w1:p1":{"exited":false,"at_shell":true}}'
o=$(OBS=$crashed tick "$prev" "$s2" 1010)
check "revive action" "$(jq -c '[.look, .revive]' <<<"$o")" '[[],[{"pane_id":"w1:p1","name":"a-w1-p1","session_id":"sid-1"}]]'
check "revive counted" "$(jq -c '.state.agents["w1:p1"] | [.revive_attempts, .last_revive_at]' <<<"$o")" '[1,1010]'
o=$(OBS='{"w1:p1":{"exited":false,"at_shell":false}}' tick "$prev" "$s2" 1010)
check "revive skipped off shell" "$(jq -c '[.revive, .log, .state.agents["w1:p1"].revive_attempts]' <<<"$o")" \
  '[[],["revive skipped: pane w1:p1 is not at a shell prompt"],1]'
o=$(OBS='{"w1:p1":{"exited":true,"at_shell":true}}' tick "$prev" "$s2" 1010)
check "deliberate exit forgotten" "$(jq -c '[.revive, .log, (.state.agents | length), .state.corps[0].agents]' <<<"$o")" \
  '[[],["forgot a-w1-p1 in w1:p1: user exited it (session sid-1)"],0,[]]'

# --- Backoff: a recent attempt suppresses another, and its pane is not read.
prev=$(jq -c '.state' <<<"$(OBS=$crashed tick "$prev" "$s2" 1010)")
o=$(tick "$prev" "$s2" 1030)
check "revive backoff" "$(jq -c '[.revive, .look]' <<<"$o")" '[[],[]]'
o=$(tick "$prev" "$s2" 1071)
check "revive after backoff looks again" "$(jq -c '.look | map(.want)' <<<"$o")" '[["exited","at_shell"]]'
check "revive after backoff" "$(OBS=$crashed tick "$prev" "$s2" 1071 | jq -c '.revive | length')" '1'

# --- Max attempts: escalate once, never revive again.
prev=$(jq -c '.agents["w1:p1"].revive_attempts = 3 | .agents["w1:p1"].last_revive_at = 1000' <<<"$prev")
o=$(tick "$prev" "$s2" 2000)
check "revive exhausted" "$(jq -c '[(.revive | length), (.look | length), (.escalate | map(.key))]' <<<"$o")" '[0,0,["revive_failed:w1:p1:1010"]]'
check "revive_failed speech" "$(jq -c '.speak | map(.text)' <<<"$o")" '["skipli crashed and could not be resumed"]'
o=$(tick "$(jq -c .state <<<"$o")" "$s2" 2010)
check "revive_failed spoken once" "$(jq -c '.speak' <<<"$o")" '[]'
check "revive_failed escalates once" "$(jq -c '.escalate | length' <<<"$o")" '0'
# The last try is not a failure until the next tick still finds it gone.
o=$(OBS=$crashed tick "$(jq -c '.agents["w1:p1"].revive_attempts = 2' <<<"$prev")" "$s2" 2000)
check "last try escalates nothing yet" "$(jq -c '[(.revive | length), .escalate, .state.agents["w1:p1"].revive_attempts]' <<<"$o")" '[1,[],3]'

# --- Herdr restart: it resumes sessions itself, briefly listing a pane that
# holds a session with no agent, and forgets names it did not assign at start.
held() { jq -cn --arg p "$1" --arg sid "$2" '{pane_id: $p, workspace_id: ($p | split(":")[0]), tab_id: (($p | split(":")[0]) + ":t1"),
  agent: null, agent_status: "unknown", agent_session: {value: $sid}, cwd: "/x", terminal_title_stripped: "t"}'; }
prev=$(jq -c .state <<<"$(tick '{}' "$(snap "$W" "[$(agent w1:p1 idle sid-1 principal)]" "[$(pane w1:p1)]")" 1000)")
o=$(tick "$prev" "$(snap "$W" '[]' "[$(held w1:p1 sid-1)]")" 1010)
check "restoring pane not revived" "$(jq -c '[(.revive | length), (.vanished | length)]' <<<"$o")" '[0,0]'
check "restoring pane kept" "$(jq -c '.state.agents["w1:p1"] | [.name, .kind, .status, .session_id]' <<<"$o")" '["principal","claude","unknown","sid-1"]'
o=$(tick "$(jq -c .state <<<"$o")" "$(snap "$W" "[$(agent w1:p1 idle sid-1)]" "[$(pane w1:p1)]")" 1020)
check "lost name restored" "$(jq -c '.rename' <<<"$o")" '[{"pane_id":"w1:p1","name":"principal","session_id":"sid-1"}]'
check "lost name kept in state" "$(jq -r '.state.agents["w1:p1"].name' <<<"$o")" 'principal'
check "named agent left alone" "$(jq -c '.rename' <<<"$(tick "$prev" "$(snap "$W" "[$(agent w1:p1 idle sid-1 principal)]" "[$(pane w1:p1)]")" 1020)")" '[]'
o=$(tick "$prev" "$(snap "$W" "[$(agent w1:p1 idle sid-2)]" "[$(pane w1:p1)]")" 1020)
check "new session keeps no name" "$(jq -c '[.rename, .state.agents["w1:p1"].name]' <<<"$o")" '[[],null]'
o=$(tick "$prev" "$(snap "$W" "[$(agent w1:p1 idle sid-1),$(agent w4:p1 idle sid-4 principal)]" "[$(pane w1:p1),$(pane w4:p1)]")" 1020)
check "taken name not restored" "$(jq -c '.rename' <<<"$o")" '[]'

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
check "spawned division speech" "$(jq -c '.speak | map(.text)' <<<"$(tick "$(jq -c .state <<<"$(tick '{}' "$st" 1000)")" "$st" 1030)")" '["dark mode in skipli is waiting on you"]'
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
o=$(INBOX='[]' DIVISIONS='{"notes":{"corps":"Skipli","worktree":true}}' tick "$(jq -c --argjson l "$flips" '.ask_log = $l' <<<"$prev")" "$sa" 2000)
check "corps and its own division are not pingpong" "$(jq -c '.escalate | map(.key)' <<<"$o")" '[]'
o=$(INBOX='[]' DIVISIONS='{"notes":{"corps":"learning","worktree":true},"skipli":{"corps":"learning","worktree":true}}' tick "$(jq -c --argjson l "$flips" '.ask_log = $l' <<<"$prev")" "$sa" 2000)
check "sibling divisions still pingpong" "$(jq -c '.escalate | map(.text)' <<<"$o")" '["asks bouncing between notes and skipli: 10 back-and-forths in the last hour"]'

# --- Divisions: nested under their Corps; their trouble goes to the Corps's Main Agent.
WD="[$(ws w1 learning),$(ws w6 tailscale),$(ws w3 berthier)]"
DIVISIONS='{"tailscale":{"corps":"learning","worktree":false}}'
sd=$(snap "$WD" "[$(agent w1:p1 idle sid-1),$(agent w6:p1 blocked sid-6),$(agent w3:p1 idle sid-b "" /home/u/berthier)]" "[$(pane w1:p1),$(pane w6:p1),$(pane w3:p1)]")
prev=$(jq -c .state <<<"$(INBOX='[]' tick '{}' "$sd" 1000)")
o=$(INBOX='[]' tick "$prev" "$sd" 1130)
check "division nested" "$(jq -c '.state.corps | map({name, divisions: (.divisions | map(.name))})' <<<"$o")" '[{"name":"learning","divisions":["tailscale"]}]'
check "division escalates to its corps" "$(jq -c '.escalate | map([.key, .to])' <<<"$o")" '[["blocked:w6:p1:1000","w1:p1"]]'
check "division speech" "$(jq -c '.speak | map(.text)' <<<"$o")" '["tailscale in learning is waiting on you"]'
check "orphan division stays a corps" "$(DIVISIONS='{}' tick "$prev" "$sd" 1130 | jq -c '.state.corps | map(.name)')" '["learning","tailscale"]'
check "division addressable" "$(INBOX='[{"id":"9","from":"skipli","to":"tailscale","text":"t","at":1100}]' tick "$prev" "$(snap "$WD" "[$(agent w1:p1 idle sid-1),$(agent w6:p1 idle sid-6)]" "[$(pane w1:p1),$(pane w6:p1)]")" 1130 | jq -c '.deliver | map(.pane_id)')" '["w6:p1"]'
check "adopted division stop is not escalated" "$(tick "$(jq -c '.agents["w6:p1"] += {status: "working", prev_status: "idle"}' <<<"$prev")" "$(snap "$WD" "[$(agent w1:p1 idle sid-1),$(agent w6:p1 idle sid-6)]" "[$(pane w1:p1),$(pane w6:p1)]")" 1130 | jq -c '.escalate')" '[]'
DIVISIONS=$D0

# --- Berthier's own Divisions: listed apart, escalations go to Berthier.
WB="[$(ws w1 skipli),$(ws w3 berthier),$(ws w7 console)]"
DIVISIONS='{"console":{"corps":"berthier","worktree":true}}'
sbd=$(snap "$WB" "[$(agent w3:p1 idle sid-b "" /home/u/berthier),$(agent w7:p1 blocked sid-7)]" "[$(pane w3:p1),$(pane w7:p1)]")
o=$(tick "$(jq -c .state <<<"$(tick '{}' "$sbd" 1000)")" "$sbd" 1130)
check "berthier divisions" "$(jq -c '[(.state.corps | map(.name)), (.state.berthier_divisions | map(.name))]' <<<"$o")" '[["skipli"],["console"]]'
check "berthier division escalates to berthier" "$(jq -c '[(.escalate | map(.to)), (.notify | length)]' <<<"$o")" '[["w3:p1"],0]'
DIVISIONS=$D0

# --- Long working.
sw=$(snap "$W" "[$(agent w1:p1 working sid-1)]" "[$(pane w1:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sw" 1000)")
check "long working" "$(jq -c '.escalate | map(.key)' <<<"$(tick "$prev" "$sw" 4600)")" '["long:w1:p1:1000"]'

# --- A spawned Division stops working: its Corps hears after settle; a Corps agent does not.
sw=$(snap "$W" "[$(agent w2:p1 working sid-2),$(agent w1:p1 working sid-1)]" "[$(pane w2:p1),$(pane w1:p1)]")
si=$(snap "$W" "[$(agent w2:p1 idle sid-2),$(agent w1:p1 idle sid-1)]" "[$(pane w2:p1),$(pane w1:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sw" 1000)")
prev=$(jq -c .state <<<"$(tick "$prev" "$si" 1100)")
check "division settle wait" "$(jq -c '.escalate | length' <<<"$(tick "$prev" "$si" 1110)")" '0'
check "division stopped" "$(jq -c '.escalate | map([.key, .to])' <<<"$(tick "$prev" "$si" 1131)")" '[["division_stopped:w2:p1:1100","w1:p1"]]'

# --- Usage limits: each idle episode is read once; nudged once after the reset.
changed() { jq -c --argjson n "$1" '. + {state_change_seq: $n}' <<<"$2"; }
sw=$(snap "$W" "[$(changed 1 "$(agent w1:p1 working sid-1 skipli)")]" "[$(pane w1:p1)]")
si=$(snap "$W" "[$(changed 2 "$(agent w1:p1 idle sid-1 skipli)")]" "[$(pane w1:p1)]")
prev=$(jq -c .state <<<"$(tick '{}' "$sw" 1000)")
check "working is not read" "$(jq -c '.look' <<<"$(tick "$prev" "$sw" 1010)")" '[]'
o=$(tick "$prev" "$si" 1100)
check "idle is read" "$(jq -c '[.look, .state.agents["w1:p1"].limit]' <<<"$o")" '[[{"pane_id":"w1:p1","session_id":"sid-1","want":["limit_reset"]}],null]'
o=$(OBS='{"w1:p1":{"limit_reset":5000}}' TZ=UTC tick "$prev" "$si" 1100)
check "limit stop recorded" "$(jq -c '[.look, .log, .resume, .state.agents["w1:p1"].limit]' <<<"$o")" \
  '[[],["usage limit: skipli in w1:p1 stopped until 1970-01-01 01:23 UTC"],[],{"episode":"2:1100","since":1100,"reset_at":5000}]'
limited=$(jq -c .state <<<"$o")
check "read once per episode" "$(jq -c '.look' <<<"$(tick "$limited" "$si" 1110)")" '[]'
check "no limit stop recorded" "$(OBS='{"w1:p1":{"limit_reset":null}}' tick "$prev" "$si" 1100 | jq -c '[.log, .state.agents["w1:p1"].limit.reset_at]')" '[[],null]'
check "no resume before reset" "$(jq -c '.resume' <<<"$(tick "$limited" "$si" 5050)")" '[]'
o=$(tick "$limited" "$si" 5060)
check "resume after reset" "$(jq -c '.resume' <<<"$o")" '[{"pane_id":"w1:p1","name":"skipli","reset_at":5000}]'
resumed=$(jq -c .state <<<"$o")
check "resume recorded" "$(jq -c '.agents["w1:p1"] | [.limit.resumed, .limit_resumed_at]' <<<"$resumed")" '[true,5060]'
check "resume once per episode" "$(jq -c '.resume' <<<"$(tick "$resumed" "$si" 5070)")" '[]'
check "resume on the tick it is read" "$(OBS='{"w1:p1":{"limit_reset":1000}}' tick "$prev" "$si" 2000 | jq -c '.resume | length')" '1'
check "working ends the episode" "$(jq -c '.resume' <<<"$(tick "$limited" "$sw" 5070)")" '[]'
# A turn between two ticks: same status, but Herdr bumped its sequence.
si3=$(snap "$W" "[$(changed 3 "$(agent w1:p1 idle sid-1 skipli)")]" "[$(pane w1:p1)]")
o=$(tick "$resumed" "$si3" 5080)
check "turn between ticks is a new episode" "$(jq -c '[(.look | map(.pane_id)), .resume]' <<<"$o")" '[["w1:p1"],[]]'
# It stopped on the limit again right after the nudge: wait out limit_retry.
again=$(OBS='{"w1:p1":{"limit_reset":5000}}' tick "$resumed" "$si3" 5080 | jq -c .state)
check "retry waits" "$(jq -c '.resume' <<<"$(tick "$again" "$si3" 6000)")" '[]'
check "retry after limit_retry" "$(jq -c '.resume | length' <<<"$(tick "$again" "$si3" 6860)")" '1'
check "gone is never resumed" "$(jq -c '.resume' <<<"$(tick "$limited" "$(snap "$W" '[]' "[$(pane w1:p1)]")" 9000)")" '[]'

# --- Corps: Divisions nested under their Corps, Berthier excluded.
o=$(tick '{}' "$si" 1000)
check "corps" "$(jq -c '.state.corps | map({name, divisions: (.divisions | map([.name, .worktree, .path]))})' <<<"$o")" \
  '[{"name":"skipli","divisions":[["dark-mode",true,"/h/dark-mode"]]},{"name":"notes","divisions":[]}]'

# --- Berthier is whoever runs in its repo (subdirs too), whatever the workspace label.
Wh="[$(ws w1 skipli),$(ws w5 hq)]"
sh=$(snap "$Wh" "[$(agent w5:p1 idle sid-h "" /home/u/berthier/tests),$(agent w1:p1 idle sid-1 "" /home/u/berthier-other)]" "[$(pane w5:p1),$(pane w1:p1)]")
o=$(tick '{}' "$sh" 1000)
check "berthier by cwd" "$(jq -c '[.state.berthier, (.state.corps | map(.name))]' <<<"$o")" '["w5:p1",["skipli"]]'

# --- Herdr's workspace order (its numbers), whatever order the snapshot lists them in.
num() { jq -c --argjson n "$1" '. + {number: $n}' <<<"$2"; }
DIVISIONS='{"dark-mode":{"corps":"skipli","worktree":true},"light-mode":{"corps":"skipli","worktree":true}}'
sn=$(snap "[$(num 4 "$(ws w4 notes)"),$(num 3 "$(ws w2 dark-mode)"),$(num 1 "$(ws w3 berthier)"),$(num 5 "$(ws w5 light-mode)"),$(num 2 "$(ws w1 skipli)")]" '[]' '[]')
o=$(tick '{}' "$sn" 1000)
check "herdr order recorded" "$(jq -c '.state.order' <<<"$o")" '["w3","w1","w2","w4","w5"]'
check "corps in herdr order" "$(jq -c '.state.corps | map([.name, (.divisions | map(.name))])' <<<"$o")" '[["skipli",["dark-mode","light-mode"]],["notes",[]]]'
DIVISIONS=$D0

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
