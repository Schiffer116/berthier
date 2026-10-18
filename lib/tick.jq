# Pure decision core of the Main Loop.
#
# Inputs (all via --argjson):
#   $prev  previous Army State ({} on first run)
#   $snap  `herdr api snapshot` .result.snapshot
#   $now   epoch seconds
#   $cfg   {berthier_root, blocked_after, long_after, division_settle, revive_max,
#           revive_backoff, revive_reset, escalation_ttl, speak_after,
#           ask_stuck, pingpong_max}
#   $divisions {division name: {corps: label, worktree: bool}} (division-new, adopt)
#   $inbox pending asks [{id, from, to, text, at}] (`berthier ask`)
#
# Output: {state, vanished: [...], revive: [...], rename: [...], escalate: [...], notify: [...],
#          speak: [...], deliver: [...]}
# The caller performs the actions; this program never touches the world.

include "names";
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
# A pane that still holds a Claude session with no agent listed is Herdr
# resuming it after a restart (a crash clears the pane's session): it is live.
| ($snap.agents // [] | map(.pane_id)) as $agent_panes
| (($snap.agents // []) + ($snap.panes // [] | map(
    select(.agent_session.value != null and (.pane_id as $p | $agent_panes | index($p) | not)))) | map(
    . as $a
    | ($prev_agents[$a.pane_id] // {}) as $p
    | ($p.status == $a.agent_status) as $same
    | ($a.agent_session.value // $p.session_id) as $sid
    | {key: $a.pane_id, value: {
        # Herdr forgets names across restarts; the name stays with its session.
        name: ($a.name // (if $sid == $p.session_id then $p.name else null end)),
        kind: ($a.agent // $p.kind),
        status: $a.agent_status,
        status_since: (if $same then $p.status_since else $now end),
        prev_status: (if $same then $p.prev_status else $p.status end),
        session_id: $sid,
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

# --- Names Herdr lost: give each listed agent back its session's name, unless
# another agent took it meanwhile.
| ($snap.agents // [] | map(.name // empty)) as $taken
| ($snap.agents // [] | map(
    select(.name == null)
    | $live[.pane_id] as $v
    | select($v.name != null and .agent_session.value == $v.session_id)
    | select($v.name as $n | $taken | index($n) | not)
    | {pane_id, name: $v.name, session_id: $v.session_id}
  )) as $rename

# --- Each Corps's Main Agent: a live Agent in the Corps's own workspace.
| ($agents | to_entries
   | map(select(.value.status != "gone"
                and (.value.workspace_label | is_division($divisions) | not)))
   | group_by(.value.workspace_label) | map({key: .[0].value.workspace_label, value: .[0].key})
   | from_entries) as $main_of

# --- Asks: deliver each to its addressee's Agent once it is ready for input,
# at most one per Agent per tick, oldest first.
| ($agents | to_entries | map(select(.value.status != "gone") | {
    pane_id: .key, status: .value.status,
    name: (if .key == $berthier then "berthier" else (.value.workspace_label | corps_name) end)
  })) as $addressable
| (reduce ($inbox | sort_by(.at))[] as $m ({used: [], deliver: []};
    . as $acc
    | ($m.to | corps_name) as $to
    | ([$addressable[] | select(.name == $to and (.status == "idle" or .status == "done"))
        | select(.pane_id as $p | $acc.used | any(. == $p) | not)] | first) as $o
    | if $o == null then . else
        .used += [$o.pane_id]
        | .deliver += [{id: $m.id, pane_id: $o.pane_id, from: $m.from, to: $to,
            text: "[berthier ask] from \($m.from): \($m.text) (Reply or report back with: berthier ask \($m.from) \"...\")"}]
      end
  ) | .deliver) as $deliver
| ((($prev.ask_log // []) | map(select(.at > $now - 3600)))
   + ($deliver | map({from, to, at: $now}))) as $ask_log

# --- Escalation candidates. Keys embed status_since so a new episode of the
# same condition escalates again, but one episode escalates only once.
| ($agents | to_entries | map(
    .key as $pane | .value as $v
    | ($now - ($v.status_since // $now)) as $age
    | ($v.workspace_label // $v.workspace_id) as $where
    | "agent '\($v.name // "-")' in \($where) (pane \($pane))" as $who
    # A Division's trouble goes to its Corps's Main Agent, which commands it.
    | (if $v.workspace_label | is_division($divisions)
       then $main_of[$divisions[$v.workspace_label | corps_name].corps // ""] else null end) as $to
    | if $v.status == "gone" and ($v.revive_attempts // 0) >= $cfg.revive_max then
        {key: "revive_failed:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name, to: $to,
         text: "revive failed \($v.revive_attempts)x: \($who), session \($v.session_id)"}
      elif $v.status == "blocked" and $age >= $cfg.blocked_after then
        {key: "blocked:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name, to: $to,
         text: "blocked \($age / 60 | floor)m on an approval or question: \($who)"}
      elif $v.status == "working" and $age >= $cfg.long_after then
        {key: "long:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name, to: $to,
         text: "working \($age / 60 | floor)m without stopping: \($who)"}
      # A spawned Division that stops may be done: its Corps reviews it.
      elif ($divisions[$v.workspace_label | corps_name].worktree // false)
           and ($v.status == "idle" or $v.status == "done")
           and $v.prev_status == "working" and $age >= $cfg.division_settle then
        {key: "division_stopped:\($pane):\($v.status_since)", pane_id: $pane, name: $v.name, to: $to,
         text: "division agent stopped working (finished or stalled): \($who)"}
      else empty end
  )
  + ($inbox | map(
      select(($now - .at) >= $cfg.ask_stuck)
      | select(.id as $id | $deliver | any(.id == $id) | not)
      | {key: "ask_stuck:\(.id)", pane_id: null,
         text: "ask from \(.from) to \(.to) undelivered \(($now - .at) / 60 | floor)m: \(.text)"}))
  # Ping-pong: replies flipping direction again and again; one-way updates don't count.
  + ($ask_log | map(. + {pair: ([.from, .to] | sort | join(" and "))}) | group_by(.pair)
     | map(sort_by(.at) as $l
       | ([range(1; $l | length) | select($l[.].from != $l[. - 1].from)] | length) as $flips
       | select($flips >= $cfg.pingpong_max)
       | {key: "pingpong:\($l[0].pair):\($now / 3600 | floor)", pane_id: null,
          text: "asks bouncing between \($l[0].pair): \($flips) back-and-forths in the last hour"}))
  | map(select($prev_escalated[.key] == null))) as $events

# --- Spoken alerts: the user is needed. Faster than escalation, because they
# skip Berthier's judgment; deduplicated per episode like escalations.
| ($agents | to_entries | map(
    .key as $pane | .value as $v
    | ($now - ($v.status_since // $now)) as $age
    | ($divisions[$v.workspace_label | corps_name].corps // null) as $parent
    | ($v.workspace_label // $v.workspace_id | spoken
       + (if $parent then " in \($parent | spoken)" else "" end)) as $where
    | if $v.status == "blocked" and $age >= $cfg.speak_after then
        {key: "speak_blocked:\($pane):\($v.status_since)", text: "\($where) is waiting on you"}
      elif $v.status == "gone" and ($v.revive_attempts // 0) >= $cfg.revive_max then
        {key: "speak_revive_failed:\($pane):\($v.status_since)", text: "\($where) crashed and could not be resumed"}
      else empty end
  ) | map(select($prev_escalated[.key] == null))) as $speak

# Berthier cannot escalate to itself: its own events go to the user instead.
| ($events | map(select(.pane_id != $berthier))) as $escalate
| ($events | map(select(.pane_id == $berthier))) as $notify

| ($prev_escalated
   | with_entries(select(.value > ($now - $cfg.escalation_ttl)))
   | . + ($events + $speak | map({key: .key, value: $now}) | from_entries)) as $escalated

# --- Corps: every workspace that is not a known Division and not Berthier's own.
| ($agents | to_entries | group_by(.value.workspace_id)
   | map({key: .[0].value.workspace_id, value: map(.key)}) | from_entries) as $by_ws
| ($workspaces | map(select(.label | is_division($divisions)))) as $division_ws
| ($workspaces
   | map(select((.workspace_id as $id | $division_ws | any(.workspace_id == $id) | not)
                 and .workspace_id != $berthier_ws and .label != "berthier"))
   | map(. as $w | {
       name: $w.label,
       workspace_id: $w.workspace_id,
       repo_root: ($w.worktree.repo_root // null),
       agents: ($by_ws[$w.workspace_id] // []),
       divisions: ($division_ws | map(select($divisions[.label | corps_name].corps == $w.label) | {
                     name: .label, workspace_id: .workspace_id,
                     worktree: $divisions[.label | corps_name].worktree,
                     path: (.worktree.checkout_path // null),
                     agents: ($by_ws[.workspace_id] // [])}))
     })) as $corps
# Berthier's own Divisions (e.g. one coding Berthier itself) hang off Berthier.
| ($workspaces | map(select(.workspace_id == $berthier_ws)) | first | .label // null) as $berthier_label
| ($division_ws | map(select($berthier_label != null and $divisions[.label | corps_name].corps == $berthier_label) | {
    name: .label, workspace_id: .workspace_id,
    worktree: $divisions[.label | corps_name].worktree,
    path: (.worktree.checkout_path // null),
    agents: ($by_ws[.workspace_id] // [])})) as $berthier_divisions

| {
    state: {
      version: 1,
      updated_at: $now,
      berthier: $berthier,
      corps: $corps,
      berthier_divisions: $berthier_divisions,
      agents: $agents,
      escalated: $escalated,
      ask_log: $ask_log
    },
    vanished: $vanished,
    revive: $revive,
    rename: $rename,
    escalate: $escalate,
    notify: $notify,
    speak: $speak,
    deliver: $deliver
  }
