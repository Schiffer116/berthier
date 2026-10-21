#!/usr/bin/env bash
# Unit tests for the weekly quota budgets: `sample` (ledger), lib/usage.jq
# (attribution) and `budget`. Fake status line JSON and state, no Herdr.
set -euo pipefail
cd "$(dirname "$0")/.."

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
b() { BERTHIER_HOME="$home" bin/berthier "$@"; }
status() { # session cwd cost week_pct [resets_at]
  jq -cn --arg s "$1" --arg c "$2" --argjson cost "$3" --argjson p "$4" --argjson r "${5:-1800000000}" \
    '{session_id: $s, cwd: $c, cost: {total_cost_usd: $cost}, rate_limits: {seven_day: {used_percentage: $p, resets_at: $r}}}'
}
sample() { status "$@" | b sample; }
ledger() { jq -c "$1" "$home/ledger.jsonl"; }

# Corps: skipli at /r/skipli with a Division at /h/dark-mode; dotfiles at /r/dot.
cat >"$home/state.json" <<'JSON'
{"berthier": "b:p1", "agents": {
  "a:p1": {"status": "idle", "workspace_label": "skipli", "cwd": "/r/skipli"},
  "d:p1": {"status": "idle", "workspace_label": "dark-mode", "cwd": "/h/dark-mode/src"},
  "c:p1": {"status": "idle", "workspace_label": ".dotfiles", "cwd": "/r/dot"},
  "b:p1": {"status": "idle", "workspace_label": "berthier", "cwd": "/r/berthier"}},
 "corps": [{"name": "skipli", "repo_root": "/r/skipli", "divisions": [{"name": "dark-mode", "path": "/h/dark-mode"}]},
           {"name": ".dotfiles", "repo_root": "/r/dot", "divisions": []}]}
JSON
echo '{"dark-mode": {"corps": "skipli", "worktree": true}}' >"$home/divisions.json"

# --- sample: silent, always exit 0, one row per change.
out=$(status s1 /r/skipli 1 10 | b sample 2>&1; echo "rc=$?")
check "sample is silent and succeeds" "$out" 'rc=0'
check "sample row" "$(ledger '[.session, .cwd, .cost, .week_pct, .resets_at]')" '["s1","/r/skipli",1,10,1800000000]'
sample s1 /r/skipli 1 10
check "unchanged render adds nothing" "$(wc -l <"$home/ledger.jsonl")" '1'
sample s1 /r/skipli 1.5 10
check "cost change adds a row" "$(wc -l <"$home/ledger.jsonl")" '2'
check "garbage never fails" "$(echo 'not json' | b sample 2>&1; echo "rc=$?")" 'rc=0'
check "no quota data, no row" "$(echo '{"session_id":"s9","cwd":"/x"}' | b sample; wc -l <"$home/ledger.jsonl")" '2'
check "unsafe session id, no row" "$(status '../x' /x 1 1 | b sample; wc -l <"$home/ledger.jsonl")" '2'
check "unwritable ledger never fails" "$(status s2 /x 1 1 | BERTHIER_HOME="$home" bash -c 'chmod 000 "$BERTHIER_HOME/ledger.jsonl"; bin/berthier sample; echo "rc=$?"; chmod 644 "$BERTHIER_HOME/ledger.jsonl"')" 'rc=0'
rm -f "$home/ledger.jsonl" "$home/usage.json"; rm -rf "$home/sampled"

# --- Concurrent samplers never tear a row.
for i in $(seq 40); do status "c$i" /x "$i" 5 | b sample & done
wait
check "concurrent rows whole" "$(jq -c .session "$home/ledger.jsonl" | wc -l)" '40'
rm -f "$home/ledger.jsonl"; rm -rf "$home/sampled"

# --- Attribution. The first row is a baseline; a rise is split by Δcost.
sample a /r/skipli 0 40
sample d /h/dark-mode 0 40
sample c /r/dot 0 40
sample a /r/skipli 3 41       # week +1, only a's cost rose: skipli
sample d /h/dark-mode 1 41    # no rise yet
sample c /r/dot 1 43          # week +2, d and c rose by 1 each: skipli (its Division) and dotfiles
sample a /r/skipli 4 44       # +1: skipli
sample c /r/dot 2 46          # +2: dotfiles
usage=$(b budget >/dev/null; cat "$home/usage.json")
check "split by corps" "$(jq -c '.by_corps | map_values(. * 1000 | round / 1000)' <<<"$usage")" '{"skipli":3,"dotfiles":3}'
check "week tracked" "$(jq -c '[.week_pct, .resets_at]' <<<"$usage")" '[46,1800000000]'
sample a /r/skipli 6 46       # a +2 and c +1.5 before the next rise of 2: 4/7 and 3/7
sample c /r/dot 3 46
sample c /r/dot 3.5 48
b budget >/dev/null
check "weighted by cost" "$(jq -c '.by_corps | map_values(. * 1000 | round / 1000)' "$home/usage.json")" '{"skipli":4.143,"dotfiles":3.857}'
sample z /somewhere/else 1 48
sample z /somewhere/else 2 50 # unmapped cwd
sample q /r/skipli 0 52       # a rise no session's cost explains
b budget >/dev/null
check "unmapped and unexplained go to other" "$(jq -c '.by_corps.other * 1000 | round / 1000' "$home/usage.json")" '4'
check "folded rows are not folded twice" "$(b budget >/dev/null; jq -c '.by_corps.skipli * 1000 | round / 1000' "$home/usage.json")" '4.143'

# --- A baseline-only record (first row, no rise yet) folds on and shows.
base=$(mktemp -d)
cp "$home/state.json" "$home/divisions.json" "$base/"
status r1 /r/dot 1 73 null | BERTHIER_HOME="$base" bin/berthier sample
check "baseline-only shows" "$(BERTHIER_HOME="$base" bin/berthier budget | head -1)" 'week resets at an unknown time, account at 73%'
echo '{"agents": {}}' >"$base/state.json"
check "baseline-only view in army" "$(BERTHIER_HOME="$base" bin/berthier army | jq -c '.budget | [.week_pct, .corps]')" '[73,[]]'
cp "$home/state.json" "$base/"
status r1 /r/dot 2 74 null | BERTHIER_HOME="$base" bin/berthier sample
check "rise after a baseline-only record" "$(BERTHIER_HOME="$base" bin/berthier budget >/dev/null; jq -c '.by_corps' "$base/usage.json")" '{"dotfiles":1}'
rm -rf "$base"

# --- A new window starts over.
sample a /r/skipli 7 3 1900000000
b budget >/dev/null
check "new window resets" "$(jq -c '[.resets_at, .week_pct, (.by_corps | map_values(. * 10 | round / 10))]' "$home/usage.json")" '[1900000000,3,{"skipli":3}]'

# --- budget: set, show, drop.
check "budget set" "$(b budget skipli 10)" 'budget for skipli: 10% of the week'
b budget .Dotfiles 5 >/dev/null
check "budgets file" "$(jq -c . "$home/budgets.json")" '{"skipli":10,"dotfiles":5}'
check "budget unknown corps" "$(b budget nope 5 2>&1)" "berthier: no Corps 'nope'; known: berthier dotfiles skipli "
check "budget bad pct" "$(b budget skipli 0 2>&1 | head -1)" 'berthier: pct must be a number in (0, 100], or off: usage: berthier budget [<corps> <pct>|off]'
check "budget bad pct 2" "$(b budget skipli lots 2>&1 | head -1 | cut -c1-30)" 'berthier: pct must be a number'
shown=$(TZ=UTC b budget)
check "budget header" "$(head -1 <<<"$shown")" "week resets $(date -u -d @1900000000 '+%a %d %b %H:%M'), account at 3%"
check "budget rows" "$(tail -n +2 <<<"$shown" | tr -s ' ')" $'corps used budget of budget\nskipli 3% 10% 30%\ndotfiles 0% 5% 0%'
b budget dotfiles off >/dev/null
check "budget dropped" "$(jq -c . "$home/budgets.json")" '{"skipli":10}'
check "budget drop unknown" "$(b budget dotfiles off 2>&1)" "berthier: no budget set for 'dotfiles'"

# --- A ledger past its size cap is moved aside once folded; nothing is lost.
big=$(mktemp -d)
cp "$home/state.json" "$home/divisions.json" "$big/"
bb() { BERTHIER_LEDGER_MAX=300 BERTHIER_HOME="$big" bin/berthier "$@"; }
for i in 1 2 3 4 5; do status r /r/dot "$i" "$((i + 10))" | BERTHIER_HOME="$big" bin/berthier sample; done
bb budget >/dev/null
check "ledger rotated" "$(test -e "$big/ledger.jsonl.prev" && echo prev)/$(test -e "$big/ledger.jsonl" && echo present || echo fresh)/$(jq .offset "$big/usage.json")" 'prev/fresh/0'
status r /r/dot 6 20 | BERTHIER_HOME="$big" bin/berthier sample
bb budget >/dev/null
check "rotation loses nothing" "$(jq -c '.by_corps | map_values(round)' "$big/usage.json")" '{"dotfiles":9}'
rm -rf "$big"

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
