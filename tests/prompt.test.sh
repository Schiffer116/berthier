#!/usr/bin/env bash
# Unit tests for input_holds, prompt_agent and start_claude, against
# tests/fake-herdr.
set -euo pipefail
cd "$(dirname "$0")/.."

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
# shellcheck source=lib/common.sh
export BERTHIER_PROMPT_POLL=0.01
BERTHIER_HOME=$home source lib/common.sh
set +e

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}
holds() { input_holds "$1" && echo yes || echo no; }
rule=──────────────────────────────────────────────────

# The ask of 2026-10-21 left in skipli's box (wJ:p2): four paths became images.
ask="[berthier ask] from ui-tweaks: PR #20 (ui-tweaks -> dev): board header Edit board pencil (owner only, offline Solo only; save reloads header+favs, delete -> /boards). Screenshots: /tmp/a.png /tmp/b.png /tmp/c.png /tmp/d.png /tmp/e.png. Non-owner case not E2E'd (needs 2nd user), same owner rule as list."
draft="● Done.
                                                                         ctrl+g to edit in Nvim
$rule
❯ [Image #4] [Image #5] [Image #6] [Image #7][berthier ask] from ui-tweaks: PR #20 (ui-tweaks -> dev): board header Edit board pencil (owner only, offline Solo only; save
  reloads header+favs, delete ->
  /boards). Screenshots: /tmp/e.png. Non-owner case not
  E2E'd (needs 2nd user), same owner rule as list.
$rule
  5h: 87% | 7d: 48% | Cost: \$0.00"
check "image draft held" "$(holds "$ask" <<<"$draft")" yes
check "text draft held" "$(holds "Reply with just: pong, then stop" <<<"$rule
❯ Reply with just:
  pong, then stop
$rule")" yes
check "long paste held" "$(holds "whatever" <<<"$rule
❯ [Pasted text #1 +40 lines]
$rule")" yes
check "empty box" "$(holds "$ask" <<<"$rule
❯
$rule")" no
check "idle suggestion is not ours" "$(holds "$ask" <<<"$rule
❯ commit this
$rule")" no
check "no box" "$(holds "$ask" <<<"● working")" no

# --- prompt_agent / start_claude against the fake Herdr.
fake=$home/herdr
mkdir -p "$fake"
cp tests/fake-herdr "$fake/herdr"
export PATH=$fake:$PATH
BERTHIER_HERDR_SESSION=
run() { # mode, then the command -> "rc: stderr | calls"
  local err rc
  echo "$1" >"$fake/mode"
  rm -f "$fake/seq" "$fake/box" "$fake/calls"
  err=$("${@:2}" 2>&1 >/dev/null)
  rc=$?
  printf '%s: %s | %s' "$rc" "$err" "$(cut -d' ' -f1-2 "$fake/calls" | grep -v "agent get" | paste -sd,)"
}
check "taken" "$(run takes prompt_agent p1 hi)" "0:  | agent prompt"
check "draft gets Enter" "$(run draft prompt_agent p1 "$ask")" "0:  | agent prompt,pane read,agent send-keys"
check "lost paste is resent" "$(run lost prompt_agent p1 hi)" "0:  | agent prompt,pane read,agent prompt"
check "busy queues it" "$(run busy prompt_agent p1 hi)" "0:  | agent prompt,pane read"
check "never taken" "$(run never prompt_agent p1 hi)" \
  "1: Claude in p1 did not take the prompt | agent prompt,pane read,agent send-keys,pane read,agent send-keys,pane read,agent send-keys,pane read"
check "herdr's message" "$(run error prompt_agent p1 hi)" "1: agent target x not found | agent prompt"

brief=$'Build it.\nThen report.'
check "brief sent as a prompt" "$(run takes start_claude demo p1 "$brief")" "0:  | agent start,agent prompt"
check "start has no brief argument" "$(grep '^agent start' "$fake/calls")" "agent start demo --kind claude --pane p1 --timeout $BERTHIER_START_TIMEOUT_MS"
check "start failure says why" "$(run nostart start_claude demo p1 "$brief")" \
  "0: agent arguments cannot be encoded safely for the target shell | agent start"
check "start failure prints failed" "$(echo nostart >"$fake/mode"; start_claude demo p1 2>/dev/null)" failed
check "undelivered brief fails" "$(echo never >"$fake/mode"; rm -f "$fake/seq"; start_claude demo p1 "$brief" 2>&1)" \
  $'Brief not delivered: Claude in p1 did not take the prompt\nfailed'

# Behind a startup dialog the Brief waits in the background for the answer.
echo dialog >"$fake/mode"
rm -f "$fake/seq" "$fake/calls" "$fake/text"
check "blocked start" "$(start_claude demo p1 "$brief" 2>&1)" blocked
for _ in $(seq 50); do [[ -s $BERTHIER_LOG ]] && break; sleep 0.1; done
check "brief sent after the dialog" "$(jq -c '[.kind, .pane, .name]' "$BERTHIER_LOG")" '["brief_sent","p1","demo"]'
check "brief prompted" "$(cat "$fake/text")" "$brief"

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
