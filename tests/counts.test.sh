#!/usr/bin/env bash
# Unit tests for `berthier counts`: a fake state.json in, bar JSON out.
set -euo pipefail
cd "$(dirname "$0")/.."

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
counts() { BERTHIER_HOME=$home bin/berthier counts; }

check "no state" "$(counts)" '{"available":false,"working":0,"blocked":0,"done":0,"idle":0,"unknown":0,"gone":0}'

echo '{"agents":{"a:p1":{"status":"working"},"b:p1":{"status":"idle"},"c:p1":{"status":"idle"},"d:p1":{"status":"blocked"}}}' >"$home/state.json"
check "fresh counts" "$(counts)" '{"available":true,"working":1,"blocked":1,"done":0,"idle":2,"unknown":0,"gone":0}'

touch -d '-5 minutes' "$home/state.json"
check "stale state" "$(counts | jq -c '[.available, .idle]')" '[false,2]'

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
