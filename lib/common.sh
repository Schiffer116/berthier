# Shared helpers for every berthier command. Sourced, never executed.
# shellcheck shell=bash disable=SC2034  # variables are consumed by the sourcing scripts

set -euo pipefail

BERTHIER_ROOT=${BERTHIER_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
# Runtime data lives in the repo (gitignored), next to the Berthier session.
BERTHIER_HOME=${BERTHIER_HOME:-$BERTHIER_ROOT/state}
BERTHIER_STATE=$BERTHIER_HOME/state.json
BERTHIER_LOG=$BERTHIER_HOME/loop.log

# Empty means the user's default Herdr session; tests point this at an
# isolated named session so they never touch live workspaces.
BERTHIER_HERDR_SESSION=${BERTHIER_HERDR_SESSION:-}

BERTHIER_INTERVAL=${BERTHIER_INTERVAL:-10}
BERTHIER_BLOCKED_AFTER=${BERTHIER_BLOCKED_AFTER:-120}
BERTHIER_LONG_AFTER=${BERTHIER_LONG_AFTER:-3600}
BERTHIER_DIVISION_SETTLE=${BERTHIER_DIVISION_SETTLE:-30}
BERTHIER_REVIVE_MAX=${BERTHIER_REVIVE_MAX:-3}
BERTHIER_REVIVE_BACKOFF=${BERTHIER_REVIVE_BACKOFF:-60}
BERTHIER_REVIVE_RESET=${BERTHIER_REVIVE_RESET:-300}
BERTHIER_ESCALATION_TTL=${BERTHIER_ESCALATION_TTL:-604800}
BERTHIER_SPEAK_AFTER=${BERTHIER_SPEAK_AFTER:-30}
# Desktop notification + chime + TTS, from the dotfiles' notify-user skill.
BERTHIER_SPEAK=${BERTHIER_SPEAK:-$HOME/.claude/skills/notify-user/speak.sh}
BERTHIER_ASK_STUCK=${BERTHIER_ASK_STUCK:-1800}
BERTHIER_PINGPONG_MAX=${BERTHIER_PINGPONG_MAX:-10}
# Usage limits: nudge a stopped Agent this long after its limit resets, and
# never twice within RETRY (a nudge that hits the limit again starts over).
BERTHIER_LIMIT_GRACE=${BERTHIER_LIMIT_GRACE:-60}
BERTHIER_LIMIT_RETRY=${BERTHIER_LIMIT_RETRY:-1800}
BERTHIER_LIMIT_PROMPT=${BERTHIER_LIMIT_PROMPT:-"The usage limit has reset. Continue exactly where you left off."}
# Restart: a gap this long since the last state write (loop down, Herdr
# unreachable) may have cut Agents off mid-turn; wait this long for each to
# reappear, then prompt it once.
BERTHIER_RESTART_GAP=${BERTHIER_RESTART_GAP:-45}
BERTHIER_RESTART_WAIT=${BERTHIER_RESTART_WAIT:-3600}
BERTHIER_RESTART_SETTLE=${BERTHIER_RESTART_SETTLE:-20}
BERTHIER_RESTART_PROMPT=${BERTHIER_RESTART_PROMPT:-"The machine restarted and interrupted your turn; continue where you left off."}
BERTHIER_INBOX=$BERTHIER_HOME/inbox
BERTHIER_REPORTS=$BERTHIER_HOME/reports
BERTHIER_DIVISIONS=$BERTHIER_HOME/divisions.json
# Weekly quota: `sample` appends to the ledger, usage_update folds it into the
# usage record, budgets.json is {corps: % of the week} (`berthier budget`).
BERTHIER_LEDGER=$BERTHIER_HOME/ledger.jsonl
BERTHIER_USAGE=$BERTHIER_HOME/usage.json
BERTHIER_BUDGETS=$BERTHIER_HOME/budgets.json
BERTHIER_LEDGER_MAX=${BERTHIER_LEDGER_MAX:-4194304}
# Share of its budget at which a Corps's Main Agent is told and the user hears (percent).
BERTHIER_BUDGET_WARN=${BERTHIER_BUDGET_WARN:-80}
# Claude model of spawned Divisions (division-new --model overrides); Main Agents and Berthier use claude's own default.
BERTHIER_DIVISION_MODEL=${BERTHIER_DIVISION_MODEL:-sonnet}
BERTHIER_START_TIMEOUT_MS=${BERTHIER_START_TIMEOUT_MS:-45000}
# How often prompt_agent checks that Claude took a prompt (seconds).
BERTHIER_PROMPT_POLL=${BERTHIER_PROMPT_POLL:-0.5}
# Claude Code's own directory: live sessions (sessions/<pid>.json) and transcripts.
CLAUDE_DIR=${CLAUDE_CONFIG_DIR:-$HOME/.claude}

mkdir -p "$BERTHIER_HOME" "$BERTHIER_INBOX" "$BERTHIER_REPORTS"

# The sessions a backgrounded ("bg") Claude session <sid> was forked from,
# nearest first, found by walking up the process tree from this process. Herdr
# knows a pane by the session it started with, so a fork's Report is filed
# under those too; the Console's chat follows the same chain the other way.
claude_forked_from() {
  local sid=$1 pid=$PPID kind="" f s k hops=0
  while ((pid > 1 && hops++ < 64)); do
    f=$CLAUDE_DIR/sessions/$pid.json
    if [[ -f $f ]] && IFS=$'\t' read -r s k < <(jq -r '[.sessionId, .kind] | @tsv' "$f"); then
      if [[ -z $kind ]]; then
        [[ $s == "$sid" ]] && kind=$k
      else
        [[ $kind == bg ]] || break
        echo "$s"
        kind=$k
      fi
    fi
    pid=$(ps -o ppid= -p "$pid" | tr -d ' ') || break
    [[ -n $pid ]] || break
  done
}

# jq with lib/names.jq available: jqn [jq args] '<program>' (include is added).
jqn() { local prog=${*: -1}; jq -L "$BERTHIER_ROOT/lib" "${@:1:$#-1}" "include \"names\"; $prog"; }
corps_name() { jqn -rn --arg s "$1" '$s | corps_name'; }

# How the Agent in a pane is addressed: berthier, its Corps, or its Division.
caller_name() {
  jqn -r --arg p "$1" 'if .berthier == $p then "berthier"
    else (.agents[$p].workspace_label // empty | corps_name) end' <"$BERTHIER_STATE"
}

# Record a Division's Corps in divisions.json ({name: {corps, worktree, model?}});
# a null entry forgets it. Atomic, so the Main Loop never reads half a file.
division_set() { # name json-or-null
  local tmp
  tmp=$(mktemp "$BERTHIER_HOME/.divisions.XXXXXX")
  jq --arg n "$1" --argjson v "$2" 'if $v == null then del(.[$n]) else .[$n] = $v end' \
    <<<"$(cat "$BERTHIER_DIVISIONS" 2>/dev/null || echo '{}')" >"$tmp"
  mv "$tmp" "$BERTHIER_DIVISIONS"
}

# The model recorded for the Division labelled $1 (empty: none, claude's default).
division_model() { # workspace-label
  jqn -r --arg l "$1" --argjson d "$(cat "$BERTHIER_DIVISIONS" 2>/dev/null || echo '{}')" \
    '$d[$l | corps_name].model // empty' <<<null
}

die() { printf 'berthier: %s\n' "$*" >&2; exit 1; }

# Append one event to loop.log, JSON Lines: {ts, kind, msg} + fields, where
# fields are whichever of pane, corps, name, session, to, why, ... the event
# has. msg is the English line; readers match on kind and fields, never msg.
# `berthier log` pretty-prints it; lib/army.jq labels each event for the Console.
event() { # kind msg [fields-json]
  jq -cn --arg kind "$1" --arg msg "$2" --argjson f "${3:-"{}"}" --argjson ts "$(date +%s)" \
    '{ts: $ts, kind: $kind} + $f + {msg: $msg}' >>"$BERTHIER_LOG"
}
# An event's fields with why it failed added.
with_why() { jq -c --arg w "$2" '. + {why: $w}' <<<"$1"; }

# Talk to Herdr from inside or outside a Herdr pane. Inherited pane context
# would otherwise pin us to whichever session spawned this process.
hd() {
  if [[ -n $BERTHIER_HERDR_SESSION ]]; then
    env -u HERDR_SOCKET_PATH herdr --session "$BERTHIER_HERDR_SESSION" "$@"
  else
    herdr "$@"
  fi
}

# Herdr agent names must match [a-z][a-z0-9_-]{0,31}.
slug() {
  local s
  s=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^[^a-z]+//; s/-+$//')
  printf '%s' "${s:0:32}"
}

# Did Claude end by a deliberate exit (/exit, Ctrl+C) rather than a crash?
# Reads its pane's tail on stdin. Only a clean exit prints its resume hint; a
# killed process leaves none. The loop reads the pane on the first tick after
# the agent vanished, so the hint is still at the bottom.
exited_deliberately() { # session id
  local tail
  tail=$(grep -v '^[[:space:]]*$' | tail -6)
  grep -qF "Resume this session with" <<<"$tail" && grep -qF "claude --resume $1" <<<"$tail"
}

# Did a session's transcript end mid-turn (a tool result, a task notification
# or a tool call nobody answered) rather than on a finished assistant turn or
# the user's own interrupt? Unknown sessions count as finished.
transcript_interrupted() { # session id
  local f last
  f=$(find "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects" -maxdepth 2 -name "$1.jsonl" -print -quit 2>/dev/null)
  [[ -n $f ]] || return 1
  last=$(tail -n 200 "$f" | jq -R -c 'fromjson? | select(.type == "user" or .type == "assistant")' | tail -n 1)
  [[ -n $last ]] && jq -e '
    if .type == "assistant" then .message.stop_reason != "end_turn"
    else (.message.content | if type == "string" then . else map(.text? // "") | join(" ") end
          | contains("[Request interrupted by user") | not) end' <<<"$last" >/dev/null
}

# When does the usage limit an idle Agent stopped on reset? Reads its pane's
# screen on stdin and prints epoch seconds, or nothing unless Claude's usage-limit
# stop is the last thing above the input box:
#   ⎿  You've hit your session limit · resets 11pm (Asia/Ho_Chi_Minh)
#   ● Usage limit reached · continuing automatically at 11pm · esc to cancel
# A bare clock is its first occurrence after since-12h: limits reset within 5h
# of the stop, and a late since (loop or Herdr restarted) still lands on it. An
# unreadable time falls back to since+5h.
usage_limit_reset() { # since (epoch the Agent went idle)
  local since=$1 lines=() rules=() i end l last="" spec="" tz="" day t
  local with_tz='resets ([^·(]+)[(]([^)]+)[)]' bare='(resets|continuing automatically at) ([^·(]+)'
  mapfile -t lines
  for i in "${!lines[@]}"; do [[ ${lines[i]} =~ ^(─){8} ]] && rules+=("$i"); done
  ((${#rules[@]} >= 2)) && lines=("${lines[@]:0:${rules[-2]}}")
  for ((i = ${#lines[@]} - 1; i >= 0; i--)); do
    l=${lines[i]}
    # Skip blanks, turn timings, the upgrade hint and right-aligned notices.
    [[ $l =~ ^[[:space:]]*$ || $l == *"· done "* || $l =~ ^[^[:alnum:]]+\ [[:alpha:]]+\ for\ [0-9] \
       || $l == *"/upgrade"* || $l =~ ^\ {20} ]] && continue
    last=$l
    break
  done
  [[ $last =~ (Usage limit reached|hit your [a-z ]*limit) ]] || return 0
  for ((end = i - 12; i >= 0 && i > end; i--)); do
    l=${lines[i]}
    if [[ $l =~ $with_tz ]]; then spec=${BASH_REMATCH[1]} tz=${BASH_REMATCH[2]}; break
    elif [[ -z $spec && $l =~ $bare ]]; then spec=${BASH_REMATCH[2]}
    fi
  done
  [[ -f /usr/share/zoneinfo/$tz ]] || tz=
  spec=${spec//,/} spec=${spec// at / }
  spec=$(sed -E 's/^ +| +$//g' <<<"$spec")
  if [[ $spec =~ ^[0-9]{1,2}(:[0-9]{2})?[ap]m$ ]]; then
    day=$(env ${tz:+"TZ=$tz"} date -d "@$((since - 43200))" +%F)
    t=$(env ${tz:+"TZ=$tz"} date -d "$day $spec" +%s)
    ((t > since - 43200)) || t=$(env ${tz:+"TZ=$tz"} date -d "$(date -d "$day +1 day" +%F) $spec" +%s)
  elif [[ -z $spec ]] || ! t=$(env ${tz:+"TZ=$tz"} date -d "$spec" +%s 2>/dev/null); then
    t=$((since + 18000))
  fi
  echo "$t"
}

# Fold the ledger rows added since last time into state/usage.json (lib/usage.jq)
# and print the record. Locked, so the Main Loop and `berthier budget` never
# fold the same rows twice. A ledger past BERTHIER_LEDGER_MAX is moved aside once
# folded: new samples start a fresh file.
usage_update() {
  local u size offset rows map moved=0
  {
    flock 8
    u=$(cat "$BERTHIER_USAGE" 2>/dev/null) || u=
    [[ -n $u ]] || u='{}'
    size=$(stat -c %s "$BERTHIER_LEDGER" 2>/dev/null) || size=0
    offset=$(jq -r '.offset // 0' <<<"$u")
    ((offset <= size)) || offset=0 # the ledger was replaced
    if ((size > offset)); then
      rows=$(tail -c "+$((offset + 1))" "$BERTHIER_LEDGER" | head -c "$((size - offset))")
      map=$(jqn -c --argjson d "$(cat "$BERTHIER_DIVISIONS" 2>/dev/null || echo '{}')" \
        'include "usage"; path_map(.; $d)' < <(cat "$BERTHIER_STATE" 2>/dev/null || echo '{}')) || map='[]'
      if ((size > BERTHIER_LEDGER_MAX)); then
        mv "$BERTHIER_LEDGER" "$BERTHIER_LEDGER.prev"
        moved=1
      fi
      u=$(jq -cnR -L "$BERTHIER_ROOT/lib" --argjson u "$u" --argjson m "$map" --argjson o "$((moved ? 0 : size))" \
        'include "usage"; $u | fold([inputs | fromjson? | objects]; $m; $o)' <<<"$rows") || return 1
      printf '%s\n' "$u" >"$BERTHIER_USAGE.tmp" && mv "$BERTHIER_USAGE.tmp" "$BERTHIER_USAGE"
    fi
    find "$BERTHIER_HOME/sampled" -type f -mtime +14 -delete 2>/dev/null || true
    printf '%s\n' "$u"
  } 8>"$BERTHIER_HOME/usage.lock"
}

# Herdr's message from its JSON error output, to stderr.
herdr_error() { { jq -er '.error.message' <<<"$1" 2>/dev/null || printf '%s\n' "$1"; } >&2; }

# Does the Claude Code input box on a pane's screen (stdin) still hold <text>,
# unsent? Claude shows pasted image paths as [Image #n] and long pastes as
# [Pasted text #n]; its idle suggestion matches neither nor the text.
input_holds() { # text
  local lines=() rules=() i box key n
  mapfile -t lines
  for i in "${!lines[@]}"; do [[ ${lines[i]} =~ ^(─){8} ]] && rules+=("$i"); done
  ((${#rules[@]} >= 2)) || return 1
  box=$(printf '%s' "${lines[@]:rules[-2]+1:rules[-1]-rules[-2]-1}")
  box=${box//[[:space:]]/} box=${box#❯}
  [[ -n $box ]] || return 1
  [[ $box == *"[Image#"* || $box == *"[Pastedtext#"* ]] && return 0
  key=${1//[[:space:]]/}
  n=$((${#key} < 24 ? ${#key} : 24))
  ((n > 0)) && [[ $box == *"${key:0:n}"* || $box == *"${key: -n}"* ]]
}

# Prompt the Agent in <pane> and make sure Claude took it: `agent prompt` only
# types the text and Enter, and Claude swallows that Enter while it turns pasted
# image paths into attachments, leaving a draft. Taken once the Agent changes
# state, or, busy, its box is empty (queued). A draft gets Enter, a lost paste
# is sent again. On failure prints why (Herdr's message) to stderr.
prompt_agent() { # pane text
  local pane=$1 text=$2 seq0 seq status err polls tries=0
  agent_state() { hd agent get "$pane" 2>/dev/null | jq -er '.result.agent | "\(.state_change_seq) \(.agent_status)"'; }
  read -r seq0 _ < <(agent_state) || { echo "no agent in $pane" >&2; return 1; }
  if ! err=$(hd agent prompt "$pane" "$text" 2>&1 >/dev/null); then
    herdr_error "$err"
    return 1
  fi
  for ((polls = 1; ; polls++)); do
    sleep "$BERTHIER_PROMPT_POLL"
    read -r seq status < <(agent_state) || { echo "agent in $pane went away" >&2; return 1; }
    [[ $seq == "$seq0" ]] || return 0
    ((polls % 4 == 0)) || continue
    if hd pane read "$pane" --source visible 2>/dev/null | input_holds "$text"; then
      ((tries++ < 3)) || break
      hd agent send-keys "$pane" enter >/dev/null 2>&1
    elif [[ $status == working ]]; then
      return 0
    else
      ((tries++ < 3)) || break
      hd agent prompt "$pane" "$text" >/dev/null 2>&1
    fi
  done
  echo "Claude in $pane did not take the prompt" >&2
  return 1
}

# Start Claude as Agent <name> in <pane>, then send the Brief with
# prompt_agent: Herdr refuses multi-line start arguments. Behind a startup
# dialog (folder trust), a background waiter sends the Brief once the user
# answers it. Prints ready, blocked or failed; why it failed goes to stderr.
start_claude() { # name pane [brief [model]]
  local err status=started
  err=$(hd agent start "$1" --kind claude --pane "$2" --timeout "$BERTHIER_START_TIMEOUT_MS" \
    ${4:+-- --model "$4"} 2>&1 >/dev/null) \
    || status=$(hd agent get "$2" 2>/dev/null | jq -r '.result.agent.agent_status // ""')
  case $status in
    blocked)
      # Detached from our stdout, or $(start_claude) would wait for it.
      if [[ -n ${3:-} ]]; then brief_after_dialog "$@" </dev/null >/dev/null 2>&1 & fi
      echo blocked ;;
    started | idle | done | working | unknown)
      # A timed-out start leaves the Agent unnamed.
      if [[ $status != started ]] && ! hd agent rename "$2" "$1" >/dev/null 2>&1; then echo failed; return; fi
      if [[ -n ${3:-} ]] && ! err=$(prompt_agent "$2" "$3" 2>&1); then
        echo "Brief not delivered: $err" >&2
        echo failed
        return
      fi
      echo ready ;;
    *)
      herdr_error "$err"
      echo failed ;;
  esac
}
brief_after_dialog() { # name pane brief
  local err f
  f=$(jq -cn --arg p "$2" --arg n "$1" '{pane: $p, name: $n}')
  if ! hd agent wait "$2" --until idle --timeout 3600000 >/dev/null 2>&1; then
    event brief_failed "Brief for $1 ($2) not sent: Claude never got past its startup dialog" \
      "$(with_why "$f" "startup dialog")"
  elif err=$(prompt_agent "$2" "$3" 2>&1); then
    event brief_sent "sent Brief to $1 ($2) after its startup dialog" "$f"
  else
    event brief_failed "Brief for $1 ($2) not sent after its startup dialog: $err" "$(with_why "$f" "$err")"
  fi
}

# Is the pane sitting at a bare interactive shell, ready for `agent start`?
pane_at_shell() {
  local name
  name=$(hd pane process-info --pane "$1" | jq -r '.result.process_info.foreground_processes[0].name // ""')
  [[ $name =~ ^-?(zsh|bash|fish|sh|dash|nu)$ ]]
}
