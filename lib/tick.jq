# Pure decision core of the Main Loop.
#
# Inputs (all via --argjson):
#   $prev  previous Fleet State ({} on first run)
#   $snap  `herdr api snapshot` .result.snapshot
#   $now   epoch seconds
#   $cfg   {berthier_root, blocked_after, long_after, task_settle, revive_max,
#           revive_backoff, revive_reset, escalation_ttl}
#
# Output: {state, vanished: [...], revive: [...], escalate: [...], notify: [...]}
# The caller performs the actions; this program never touches the world.

def task_prefix: "󱎦 ";
def is_task_label: (. // "") | startswith(task_prefix);
# Berthier is whichever agent runs in Berthier's own repository.
def is_berthier_cwd: (. // "") as $c | $c == $cfg.berthier_root or ($c | startswith($cfg.berthier_root + "/"));

# Herdr agent names must match [a-z][a-z0-9_-]{0,31}.
def fallback_name($pane): "a-" + ($pane | ascii_downcase | gsub("[^a-z0-9]"; "-"));

($snap.workspaces // []) as $workspaces
| ($workspaces | map({key: .workspace_id, value: .}) | from_entries) as $ws
| ($snap.panes // [] | map(.pane_id)) as $pane_ids
| ($prev.agents // {}) as $prev_agents
| ($prev.escalated // {}) as $prev_escalated

# --- Live agents: carry over memory (session id, timers, revive bookkeeping).
| ($snap.agents // [] | map(
    . as $a
    | ($prev_agents[$a.pane_id] // {}) as $p
    | ($p.status == $a.agent_status) as $same
    | {key: $a.pane_id, value: {
        name: ($a.name // $p.name),
        kind: $a.agent,
        status: $a.agent_status,
        status_since: (if $same then $p.status_since else $now end),
        prev_status: (if $same then $p.prev_status else $p.status end),
        session_id: ($a.agent_session.value // $p.session_id),
        workspace_id: $a.workspace_id,
        workspace_label: $ws[$a.workspace_id].label,
        tab_id: $a.tab_id,
        cwd: $a.cwd,
        title: $a.terminal_title_stripped,
        revive_attempts: (if ($p.last_revive_at // 0) > ($now - $cfg.revive_reset)
                          then ($p.revive_attempts // 0) else 0 end),
        last_revive_at: $p.last_revive_at
      }}
  ) | from_entries) as $live

# --- Agents that vanished. A pane still present means the process died and
# left its shell behind: keep it as gone and try to Revive. A closed pane is
# a deliberate stop: forget the agent.
| ($prev_agents | to_entries | map(
    select($live[.key] == null)
    | select(.value.session_id != null and .value.kind == "claude")
    | select(.key as $k | $pane_ids | index($k))
    | .value |= (
        (if .status == "gone" then . else
          . + {prev_status: .status, status: "gone", status_since: $now}
         end)
        | .workspace_label = ($ws[.workspace_id].label // .workspace_label)
      )
  ) | from_entries) as $gone

| ($live + $gone) as $agents
| ($agents | to_entries | map(select(.value.cwd | is_berthier_cwd))
   | (map(select(.value.status != "gone")) + .) | first | .key? // null) as $berthier
| (if $berthier then $agents[$berthier].workspace_id else null end) as $berthier_ws

# --- Agents that vanished this very tick: the caller checks each for a
# deliberate exit while the evidence is still at the bottom of its pane.
| ($gone | to_entries | map(
    select($prev_agents[.key].status != "gone")
    | {pane_id: .key, name: (.value.name // fallback_name(.key)), session_id: .value.session_id}
  )) as $vanished

# --- Revive actions.
| ($gone | to_entries | map(
    select((.value.revive_attempts // 0) < $cfg.revive_max)
    | select(($now - (.value.last_revive_at // 0)) >= $cfg.revive_backoff)
    | {pane_id: .key,
       name: (.value.name // fallback_name(.key)),
       session_id: .value.session_id}
  )) as $revive

# --- Escalation candidates. Keys embed status_since so a new episode of the
# same condition escalates again, but one episode escalates only once.
| ($agents | to_entries | map(
    .key as $pane | .value as $v
    | ($now - ($v.status_since // $now)) as $age
    | ($v.workspace_label // $v.workspace_id) as $where
    | "agent '\($v.name // "-")' in \($where) (pane \($pane))" as $who
    | if $v.status == "gone" and ($v.revive_attempts // 0) >= $cfg.revive_max then
        {key: "revive_failed:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name,
         text: "revive failed \($v.revive_attempts)x: \($who), session \($v.session_id)"}
      elif $v.status == "blocked" and $age >= $cfg.blocked_after then
        {key: "blocked:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name,
         text: "blocked \($age / 60 | floor)m on an approval or question: \($who)"}
      elif $v.status == "working" and $age >= $cfg.long_after then
        {key: "long:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name,
         text: "working \($age / 60 | floor)m without stopping: \($who)"}
      elif ($v.workspace_label | is_task_label)
           and ($v.status == "idle" or $v.status == "done")
           and $v.prev_status == "working" and $age >= $cfg.task_settle then
        {key: "task_stopped:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name,
         text: "task agent stopped working (finished or stalled): \($who)"}
      else empty end
  ) | map(select($prev_escalated[.key] == null))) as $events

# Berthier cannot escalate to itself: its own events go to the user instead.
| ($events | map(select(.pane_id != $berthier))) as $escalate
| ($events | map(select(.pane_id == $berthier))) as $notify

| ($prev_escalated
   | with_entries(select(.value > ($now - $cfg.escalation_ttl)))
   | . + ($events | map({key: .key, value: $now}) | from_entries)) as $escalated

# --- Projects: every workspace that is not a Task and not Berthier's own.
| ($agents | to_entries | group_by(.value.workspace_id)
   | map({key: .[0].value.workspace_id, value: map(.key)}) | from_entries) as $by_ws
| ($workspaces | map(select(.label | is_task_label))) as $task_ws
| ($workspaces
   | map(select((.label | is_task_label | not) and .workspace_id != $berthier_ws and .label != "berthier"))
   | map(. as $w | {
       name: $w.label,
       workspace_id: $w.workspace_id,
       repo_root: ($w.worktree.repo_root // null),
       agents: ($by_ws[$w.workspace_id] // []),
       tasks: ($task_ws | map(select(
                 (($w.worktree.repo_key // null) != null and .worktree.repo_key == $w.worktree.repo_key)
                 or ((.label | ltrimstr(task_prefix) | split("/")[0]) == $w.label)
               ) | {
                 name: (.label | ltrimstr(task_prefix)),
                 workspace_id: .workspace_id,
                 path: (.worktree.checkout_path // null),
                 agents: ($by_ws[.workspace_id] // [])
               }))
     })) as $projects

| {
    state: {
      version: 1,
      updated_at: $now,
      berthier: $berthier,
      projects: $projects,
      agents: $agents,
      escalated: $escalated
    },
    vanished: $vanished,
    revive: $revive,
    escalate: $escalate,
    notify: $notify
  }
