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
{"berthier": "b:p1", "agents": {
  "a:p1": {"status": "working", "session_id": "s-a", "workspace_label": "skipli"},
  "b:p1": {"status": "idle", "session_id": "s-b", "workspace_label": "berthier"},
  "c:p1": {"status": "idle", "session_id": "s-c", "workspace_label": ".dotfiles"},
  "d:p1": {"status": "blocked", "session_id": "s-d", "workspace_label": "󱎦 skipli/dark-mode"}}}
JSON
check "fresh counts" "$(counts | jq -c 'del(.report)')" '{"available":true,"working":1,"blocked":1,"done":0,"idle":2,"unknown":0,"gone":0}'

mkdir -p "$home/reports"
echo '{"line": "building offline sync"}' >"$home/reports/s-a.json"
echo '{"line": "Needs you: push main?"}' >"$home/reports/s-c.json"
echo '{"line": "watching the Army"}' >"$home/reports/s-b.json"
echo '{"line": "an old session"}' >"$home/reports/s-gone.json"
check "report" "$(counts | jq -r .report)" $'dotfiles: needs you - push main?\nberthier: idle - watching the Army\nskipli: working - building offline sync'

touch -d '-5 minutes' "$home/state.json"
check "stale state" "$(counts | jq -c '[.available, .idle]')" '[false,2]'

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
