#!/usr/bin/env bash
# Unit tests for report, hook-stop and ask: fake state and transcripts, no Herdr.
set -euo pipefail
cd "$(dirname "$0")/.."

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
b() { env -u HERDR_PANE_ID -u CLAUDE_CODE_SESSION_ID BERTHIER_HOME="$home" "$@"; }
cat >"$home/state.json" <<'JSON'
{"berthier": "b:p1", "agents": {
  "a:p1": {"status": "idle", "workspace_label": "skipli"},
  "b:p1": {"status": "working", "workspace_label": "berthier"},
  "c:p1": {"status": "idle", "workspace_label": ".dotfiles"},
  "d:p1": {"status": "gone", "workspace_label": "old"}}}
JSON

# --- report
b bin/berthier report "outside herdr"
check "report no-op outside herdr" "$(find "$home/reports" -name '*.json' | wc -l)" '0'
b HERDR_PANE_ID=a:p1 CLAUDE_CODE_SESSION_ID=s-a bin/berthier report $'needs you:\tpush main?\n'
check "report written" "$(jq -c '[.line, .pane]' "$home/reports/s-a.json")" '["needs you: push main?","a:p1"]'
# A backgrounded fork's Report is also filed under the session it was forked
# from (an ancestor process), which is the one Herdr and the Grand Army State
# know.
claude="$home/claude"
mkdir -p "$claude/sessions"
session_file() { echo "{\"pid\": $1, \"sessionId\": \"$2\", \"kind\": \"$3\"}" >"$claude/sessions/$1.json"; }
session_file $$ s-root interactive
fork_report() { # kind of the forked session, then report's args
  b CLAUDE_CONFIG_DIR="$claude" HERDR_PANE_ID=a:p1 CLAUDE_CODE_SESSION_ID=s-fork \
    bash -c 'echo "{\"pid\": $$, \"sessionId\": \"s-fork\", \"kind\": \"$0\"}" >"$1/sessions/$$.json"; bin/berthier report "$2"; rm "$1/sessions/$$.json"' "$1" "$claude" "$2"
}
fork_report bg "forked work"
check "fork report under its own session" "$(jq -r .line "$home/reports/s-fork.json")" 'forked work'
check "fork report under the pane's session" "$(jq -r .line "$home/reports/s-root.json")" 'forked work'
rm "$home/reports/s-root.json"
fork_report interactive "nested claude"
check "nested non-bg session stays its own" "$(jq -r .line "$home/reports/s-fork.json")/$(test -e "$home/reports/s-root.json" && echo leaked)" 'nested claude/'

# --- hook-stop
tr="$home/t.jsonl"
cat >"$tr" <<'JSONL'
{"type":"user","timestamp":"2020-01-01T00:00:00.000Z","message":{"content":"old prompt"}}
{"type":"user","timestamp":"2099-01-01T00:00:00.000Z","message":{"content":"do the thing"}}
{"type":"user","timestamp":"2099-01-01T00:00:05.000Z","message":{"content":[{"type":"tool_result"}]}}
JSONL
hook() { b HERDR_PANE_ID=a:p1 bin/berthier hook-stop <<<"$1"; }
check "hook blocks unreported turn" "$(hook "{\"session_id\":\"s-a\",\"transcript_path\":\"$tr\"}" | jq -r .decision)" 'block'
check "hook lets a retry stop" "$(hook "{\"session_id\":\"s-a\",\"transcript_path\":\"$tr\",\"stop_hook_active\":true}")" ''
check "hook ignores non-herdr" "$(b bin/berthier hook-stop <<<"{\"session_id\":\"s-a\",\"transcript_path\":\"$tr\"}")" ''
sed -i 's/2099-01-01T00:00:0[05]/2020-01-02T00:00:00/' "$tr"
check "hook passes reported turn" "$(hook "{\"session_id\":\"s-a\",\"transcript_path\":\"$tr\"}")" ''

# --- ask
check "ask needs a pane" "$(b bin/berthier ask skipli hi 2>&1)" 'berthier: run this from an Agent'"'"'s Herdr pane (no HERDR_PANE_ID)'
check "ask unknown corps" "$(b HERDR_PANE_ID=a:p1 bin/berthier ask old hi 2>&1)" "berthier: no Corps 'old'; known: berthier dotfiles skipli "
b HERDR_PANE_ID=a:p1 bin/berthier ask .Dotfiles $'add a\nunit' >/dev/null
check "ask queued" "$(jq -c '[.from, .to, .text]' "$home"/inbox/*.json)" '["skipli","dotfiles","add a unit"]'
b HERDR_PANE_ID=b:p1 bin/berthier ask skipli "all done" >/dev/null
check "berthier asks as berthier" "$(jq -r 'select(.to == "skipli") | .from' "$home"/inbox/*.json)" 'berthier'

# --- notify: no pane needed, one-way, capped, multi-line kept
check "notify unknown corps" "$(b bin/berthier notify old hi 2>&1)" "berthier: no Corps 'old'"
rm -f "$home"/inbox/*.json
b bin/berthier notify .Skipli $'answer:\n42' >/dev/null
check "notify queued" "$(jq -c '[.kind, .from, .to, .text]' "$home"/inbox/*.json)" '["notify","service","skipli","answer:\n42"]'
rm -f "$home"/inbox/*.json
b BERTHIER_NOTIFY_MAX=5 bin/berthier notify skipli abcdefgh >/dev/null
check "notify capped" "$(jq -r .text "$home"/inbox/*.json)" 'abcde [truncated]'
rm -f "$home"/inbox/*.json

# --- loop: a Herdr outage logs once, not every tick.
lh=$(mktemp -d)
echo "2026-10-17T13:02:57+07:00 loop started (interval 10s, session default)" >"$lh/loop.log"
BERTHIER_HOME=$lh BERTHIER_INTERVAL=0.2 BERTHIER_HERDR_SESSION=berthier-no-such-session timeout 2 bin/berthier loop 2>/dev/null || true
check "outage logged once" "$(jq -c 'select(.kind == "loop_error") | .why' "$lh/loop.log")" '"herdr unreachable"'
check "prose log set aside" "$(cat "$lh/loop.log.pre-json")" '2026-10-17T13:02:57+07:00 loop started (interval 10s, session default)'
check "log pretty-prints" "$(BERTHIER_HOME=$lh bin/berthier log -n 1 | cut -c22-)" 'loop_stopped     loop stopped'
rm -rf "$lh"

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
