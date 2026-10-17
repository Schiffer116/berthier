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
 "corps": [{"name": "skipli", "agents": ["a:p1"], "divisions": [{"name": "󱎦 dark-mode", "worktree": true, "agents": ["d:p1"]}]},
           {"name": ".dotfiles", "agents": ["c:p1"], "divisions": []}],
 "agents": {
  "a:p1": {"status": "working", "session_id": "s-a", "workspace_label": "skipli"},
  "b:p1": {"status": "idle", "session_id": "s-b", "workspace_label": "berthier"},
  "c:p1": {"status": "idle", "session_id": "s-c", "workspace_label": ".dotfiles"},
  "d:p1": {"status": "blocked", "session_id": "s-d", "workspace_label": "󱎦 dark-mode"}}}
JSON
check "fresh counts" "$(counts | jq -c 'del(.report)')" '{"available":true,"working":1,"blocked":1,"done":0,"idle":2,"unknown":0,"gone":0}'

mkdir -p "$home/reports"
echo '{"line": "building offline sync"}' >"$home/reports/s-a.json"
echo '{"line": "Needs you: push main?"}' >"$home/reports/s-c.json"
echo '{"line": "watching the Army"}' >"$home/reports/s-b.json"
echo '{"line": "an old session"}' >"$home/reports/s-gone.json"
check "report" "$(counts | jq -r .report)" $'dark-mode: needs you - no report yet\ndotfiles: needs you - push main?\nberthier: idle - watching the Army\nskipli: working - building offline sync'

# --- army: the full view model behind counts and the dashboard.
army() { BERTHIER_HOME=$home bin/berthier army; }
printf '2026-10-17T13:00:00+07:00 delivered ask 1 to c:p1\n2026-10-17T13:05:00+07:00 spoke: skipli is waiting on you\n' >"$home/loop.log"
mkdir -p "$home/inbox"; echo '{"from":"skipli","to":"dotfiles","text":"unit","at":5}' >"$home/inbox/7-skipli.json"
check "army needs you" "$(army | jq -c '.needs_you | map(.who)')" '["dark-mode","dotfiles"]'
check "army division nested" "$(army | jq -c '.corps[0].divisions[0] | [.name, .worktree, .agents[0].status]')" '["dark-mode",true,"blocked"]'
check "army activity newest first" "$(army | jq -c '.activity | map(.text)')" '["spoke: skipli is waiting on you","delivered ask 1 to c:p1"]'
check "army asks" "$(army | jq -c '.asks | map([.id, .to])')" '[["7-skipli","dotfiles"]]'
check "army berthier" "$(army | jq -c '.berthier | [.who, .report]')" '["berthier","watching the Army"]'

touch -d '-5 minutes' "$home/state.json"
check "stale state" "$(counts | jq -c '[.available, .idle]')" '[false,2]'

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
