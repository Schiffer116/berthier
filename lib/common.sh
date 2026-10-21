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
BERTHIER_INBOX=$BERTHIER_HOME/inbox
BERTHIER_REPORTS=$BERTHIER_HOME/reports
BERTHIER_DIVISIONS=$BERTHIER_HOME/divisions.json
BERTHIER_START_TIMEOUT_MS=${BERTHIER_START_TIMEOUT_MS:-45000}
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

# Record a Division's Corps in divisions.json ({name: {corps, worktree}});
# a null entry forgets it. Atomic, so the Main Loop never reads half a file.
division_set() { # name json-or-null
  local tmp
  tmp=$(mktemp "$BERTHIER_HOME/.divisions.XXXXXX")
  jq --arg n "$1" --argjson v "$2" 'if $v == null then del(.[$n]) else .[$n] = $v end' \
    <<<"$(cat "$BERTHIER_DIVISIONS" 2>/dev/null || echo '{}')" >"$tmp"
  mv "$tmp" "$BERTHIER_DIVISIONS"
}

die() { printf 'berthier: %s\n' "$*" >&2; exit 1; }

log() { printf '%s %s\n' "$(date -Is)" "$*" >>"$BERTHIER_LOG"; }

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
# Only a clean exit prints its resume hint; a killed process leaves none. The
# loop checks on the first tick after the agent vanished, so the hint is still
# at the bottom of the pane.
exited_deliberately() {
  local tail
  tail=$(hd pane read "$1" --source recent-unwrapped --lines 20 | grep -v '^[[:space:]]*$' | tail -6)
  grep -qF "Resume this session with" <<<"$tail" && grep -qF "claude --resume $2" <<<"$tail"
}

# When does the usage limit an idle Agent stopped on reset? Reads its pane's
# tail on stdin and prints epoch seconds, or nothing unless Claude's usage-limit
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

# Start Claude as Agent <name> in <pane>, the Brief riding along as its first
# prompt so it is delivered even past a folder trust dialog. Herdr's start waits
# out that whole first turn, so a long Brief times out a healthy start and
# leaves the Agent unnamed: name it then. Prints ready, blocked or failed.
start_claude() { # name pane [brief]
  hd agent start "$1" --kind claude --pane "$2" --timeout "$BERTHIER_START_TIMEOUT_MS" \
    -- ${3:+"$3"} >/dev/null 2>&1 && { echo ready; return; }
  case $(hd agent get "$2" 2>/dev/null | jq -r '.result.agent.agent_status // ""') in
    blocked) echo blocked ;;
    working | idle | done | unknown) hd agent rename "$2" "$1" >/dev/null 2>&1 && echo ready || echo failed ;;
    *) echo failed ;;
  esac
}

# Is the pane sitting at a bare interactive shell, ready for `agent start`?
pane_at_shell() {
  local name
  name=$(hd pane process-info --pane "$1" | jq -r '.result.process_info.foreground_processes[0].name // ""')
  [[ $name =~ ^-?(zsh|bash|fish|sh|dash|nu)$ ]]
}
