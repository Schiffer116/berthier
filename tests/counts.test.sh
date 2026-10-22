#!/usr/bin/env bash
# Unit tests for `berthier counts`: a fake state.json and reports in, bar JSON out.
set -euo pipefail
cd "$(dirname "$0")/.."

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
counts() { BERTHIER_HOME=$home bin/berthier counts; }

check "no state" "$(counts)" '{"available":false,"working":0,"blocked":0,"done":0,"idle":0,"unknown":0,"gone":0,"report":""}'

cat >"$home/state.json" <<'JSON'
{"berthier": "b:p1",
 "corps": [{"name": "skipli", "agents": ["a:p1"], "divisions": [{"name": "dark-mode", "worktree": true, "agents": ["d:p1"]}]},
           {"name": ".dotfiles", "agents": ["c:p1"], "divisions": []}],
 "agents": {
  "a:p1": {"status": "working", "session_id": "s-a", "workspace_label": "skipli"},
  "b:p1": {"status": "idle", "session_id": "s-b", "workspace_label": "berthier"},
  "c:p1": {"status": "idle", "session_id": "s-c", "workspace_label": ".dotfiles"},
  "d:p1": {"status": "blocked", "session_id": "s-d", "workspace_label": "dark-mode"}}}
JSON
check "fresh counts" "$(counts | jq -c 'del(.report)')" '{"available":true,"working":1,"blocked":1,"done":0,"idle":2,"unknown":0,"gone":0}'

mkdir -p "$home/reports"
echo '{"line": "building offline sync"}' >"$home/reports/s-a.json"
echo '{"line": "Needs you: push main?"}' >"$home/reports/s-c.json"
echo '{"line": "watching the Grand Army"}' >"$home/reports/s-b.json"
echo '{"line": "an old session"}' >"$home/reports/s-gone.json"
check "report" "$(counts | jq -r .report)" $'dark-mode: needs you - no report yet\ndotfiles: needs you - push main?\nberthier: idle - watching the Grand Army\nskipli: working - building offline sync'

# --- army: the full view model behind counts and the dashboard.
army() { BERTHIER_HOME=$home bin/berthier army; }
now=$(date +%s)
ev() { jq -cn --argjson ts "$((now - $1))" --arg kind "$2" --argjson f "$3" '{ts: $ts, kind: $kind} + $f + {msg: "\($kind) ..."}'; }
{
  ev $((8 * 86400)) escalated '{"why":"long","to":"berthier","reason":"too old"}'
  echo "2026-10-17T13:02:57+07:00 an old prose line"
  ev 600 loop_started '{"interval":10}'
  ev 300 delivered '{"pane":"c:p1","from":"skipli","to":"dotfiles"}'
  ev 200 spoke '{"pane":"a:p1","corps":"skipli","why":"blocked","to":"you"}' | jq -c '.msg = "spoke: skipli is waiting on you."'
  ev 100 notified '{"pane":"b:p1","corps":"berthier","why":"blocked","to":"you","reason":"blocked 2m on an approval or question"}'
  ev 100 escalated '{"pane":"d:p1","corps":"dark-mode","why":"blocked","to":"skipli","reason":"blocked 3m on an approval or question"}'
  ev 100 escalated '{"why":"pingpong","to":"berthier","ask_from":"dotfiles","ask_to":"skipli","reason":"10 back-and-forths in the last hour"}'
  ev 100 escalated '{"why":"ask_stuck","to":"berthier","ask_from":"skipli","ask_to":"notes","reason":"undelivered 31m"}'
} >"$home/loop.log"
mkdir -p "$home/inbox"; echo '{"from":"skipli","to":"dotfiles","text":"unit","at":5}' >"$home/inbox/7-skipli.json"
echo '{"sessions": {"s-d": {"cost": 1.5}}}' >"$home/usage.json"
echo '{"dark-mode": {"corps": "skipli", "worktree": true, "model": "opus"}}' >"$home/divisions.json"
check "army session cost and division model" "$(army | jq -c '[(.corps[0].divisions[0].agents[0], .corps[0].agents[0]) | [.cost, .division_model]]')" '[[1.5,"opus"],[null,null]]'
rm "$home/usage.json" "$home/divisions.json"
check "army needs you" "$(army | jq -c '.needs_you | map(.who)')" '["dark-mode","dotfiles"]'
check "army division nested" "$(army | jq -c '.corps[0].divisions[0] | [.name, .worktree, .agents[0].status]')" '["dark-mode",true,"blocked"]'
check "army activity newest first" "$(army | jq -c '.activity[2:6] | map([.tag, .label])')" '[["escal","escal"],["escal","escal"],["speak","speak"],["ask","ask"]]'
check "army log times" "$(army | jq -c --argjson n "$now" '[.loop.started_at - $n, .activity[0].at - $n]')" '[-600,-100]'
check "army escalations 7d" "$(army | jq -c '.escalations | map([.label, .to, .who, .why])')" \
  '[["ask stuck","berthier","skipli → notes","undelivered 31m"],["pingpong","berthier","dotfiles ⇄ skipli","10 back-and-forths in the last hour"],["blocked","skipli","dark-mode d:p1","blocked 3m on an approval or question"],["blocked","you","berthier b:p1","blocked 2m on an approval or question"],["speak","you","skipli","skipli is waiting on you."]]'
check "army revive max" "$(army | jq -c .loop.revive_max)" '3'
check "army asks" "$(army | jq -c '.asks | map([.id, .to])')" '[["7-skipli","dotfiles"]]'
check "army berthier" "$(army | jq -c '.berthier | [.who, .report]')" '["berthier","watching the Grand Army"]'
check "army units without an order" "$(army | jq -c '.units | map([.kind, .name])')" '[["corps","skipli"],["corps","dotfiles"],["berthier","berthier"]]'
# Herdr's order: Berthier's workspace first here, Divisions sorted under their Corps.
jq '.order = ["wb", "wc", "wa", "we", "wd"] | .corps[0].workspace_id = "wa" | .corps[1].workspace_id = "wc"
    | .corps[0].divisions = [{"name": "z-div", "workspace_id": "we", "agents": []}] + (.corps[0].divisions | map(.workspace_id = "wd"))
    | .agents["b:p1"].workspace_id = "wb"' "$home/state.json" >"$home/s" && mv "$home/s" "$home/state.json"
check "army units in herdr order" "$(army | jq -c '.units | map([.name, (.divisions | map(.name))])')" '[["berthier",[]],["dotfiles",[]],["skipli",["z-div","dark-mode"]]]'

touch -d '-5 minutes' "$home/state.json"
check "stale state" "$(counts | jq -c '[.available, .idle]')" '[false,2]'

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
