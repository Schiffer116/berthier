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
BERTHIER_TASK_SETTLE=${BERTHIER_TASK_SETTLE:-30}
BERTHIER_REVIVE_MAX=${BERTHIER_REVIVE_MAX:-3}
BERTHIER_REVIVE_BACKOFF=${BERTHIER_REVIVE_BACKOFF:-60}
BERTHIER_REVIVE_RESET=${BERTHIER_REVIVE_RESET:-300}
BERTHIER_ESCALATION_TTL=${BERTHIER_ESCALATION_TTL:-604800}
BERTHIER_START_TIMEOUT_MS=${BERTHIER_START_TIMEOUT_MS:-45000}

TASK_PREFIX="󱎦 "

mkdir -p "$BERTHIER_HOME"

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

# Is the pane sitting at a bare interactive shell, ready for `agent start`?
pane_at_shell() {
  local name
  name=$(hd pane process-info --pane "$1" | jq -r '.result.process_info.foreground_processes[0].name // ""')
  [[ $name =~ ^-?(zsh|bash|fish|sh|dash|nu)$ ]]
}
