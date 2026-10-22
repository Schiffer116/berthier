# Pure decision core of the Main Loop.
#
# Inputs (all via --argjson):
#   $prev  previous Grand Army State ({} on first run)
#   $snap  `herdr api snapshot` .result.snapshot
#   $now   epoch seconds
#   $cfg   {berthier_root, blocked_after, long_after, division_settle, revive_max,
#           revive_backoff, revive_reset, escalation_ttl, speak_after,
#           ask_stuck, pingpong_max, limit_grace, limit_retry, budget_warn}
#   $divisions {division name: {corps: label, worktree: bool}} (division-new, adopt)
#   $inbox pending asks [{id, from, to, text, at}] (`berthier ask`)
#   $usage, $budgets  optional: the quota usage record (state/usage.json, lib/usage.jq)
#          and {corps: % of the week} (state/budgets.json)
#   $obs   optional: what the caller read from panes this program asked to look
#          at, {pane: {exited, at_shell, limit_reset}}
#
# Output: {state, look: [...], events: [...], revive: [...], rename: [...], resume: [...],
#          escalate: [...], notify: [...], speak: [...], deliver: [...]}
# events are this program's decisions for loop.log ({kind, msg, ...fields}, see
# `event` in lib/common.sh); each action carries ev, the fields of the event
# the caller logs for its outcome.
# A decision that needs a pane read waits: the pane is listed under look
# ({pane_id, session_id, want: [exited, at_shell, limit_reset]}) and the caller
# runs this again with $obs. This program alone writes the Grand Army State; the
# caller performs the actions and never touches the state.

include "names";
include "usage";
# The Corps a workspace label counts toward: a Division's Corps, else itself.
def corps_of_label: owner($divisions);

# Berthier is whichever agent runs in Berthier's own repository.
def is_berthier_cwd: (. // "") as $c | $c == $cfg.berthier_root or ($c | startswith($cfg.berthier_root + "/"));

# Herdr agent names must match [a-z][a-z0-9_-]{0,31}.
def fallback_name($pane): "a-" + ($pane | ascii_downcase | gsub("[^a-z0-9]"; "-"));

# Has the caller read this pane for $what?
def seen($what): . as $p | ($ARGS.named.obs // {})[$p] // {} | has($what);

($ARGS.named.obs // {}) as $obs
# Herdr's own workspace order, which every view of the Grand Army follows.
| ($snap.workspaces // [] | sort_by(.number // 0)) as $workspaces
| ($workspaces | map({key: .workspace_id, value: .}) | from_entries) as $ws
| ($snap.panes // [] | map(.pane_id)) as $pane_ids
# --- Weekly budgets: a Corps at its allowance has its Revives, usage-limit
# resumes and ask deliveries held (soft: nothing is stopped), and its Main Agent
# and the user hear at the warning level, once per week.
| usage_view($ARGS.named.usage // {}; $ARGS.named.budgets // {}; $now).corps
  | map(select(.allowance != null and .allowance > 0)) as $budget
| (($ARGS.named.usage // {}).resets_at // 0) as $week
| ($budget | map(select(.ratio >= 1) | .name)) as $over
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
        # Herdr bumps it on every status change, even a turn between two ticks.
        state_seq: $a.state_change_seq,
        # The caller's reading of an idle episode for a usage-limit stop:
        # {episode, since, reset_at, resumed}; reset_at null means no limit stop.
        limit: $p.limit,
        limit_resumed_at: $p.limit_resumed_at,
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

# --- Agents that vanished this very tick, and gone ones due a Revive.
| ($gone | keys | map(select($prev_agents[.].status != "gone"))) as $vanished
| ($gone | to_entries | map(
    select((.value.revive_attempts // 0) < $cfg.revive_max)
    | select(.value.workspace_label | corps_of_label | IN($over[]) | not)
    | select(($now - (.value.last_revive_at // 0)) >= $cfg.revive_backoff) | .key)) as $due
# A clean exit is the user's choice: forget the agent instead of Reviving.
# Read when it vanishes, while the evidence is still at the bottom of its
# pane, and again right before any Revive.
| ($vanished + $due | unique) as $exit_check
| ($exit_check | map(select($obs[.].exited == true))) as $exited
| ($gone | with_entries(select(.key as $k | $exited | index($k) | not))) as $gone
| ($due | map(select($obs[.].exited == false and seen("at_shell")))) as $reviving

| ($live + $gone) as $agents
| ($agents | to_entries | map(select(.value.cwd | is_berthier_cwd))
   | (map(select(.value.status != "gone")) + .) | first | .key? // null) as $berthier
| (if $berthier then $agents[$berthier].workspace_id else null end) as $berthier_ws
# Who an event is about, by pane.
| def about($p): ($agents[$p] // $prev_agents[$p]) as $a
    | {pane: $p, corps: (if $p == $berthier then "berthier" else $a.workspace_label | corps_name end),
       name: ($a.name // fallback_name($p))};
  .

# --- Revive: resume the gone session in the pane it died in, if the pane is
# back at its shell. Every try counts towards revive_max.
| ($reviving | map(select($obs[.].at_shell)
    | {pane_id: ., name: ($agents[.].name // fallback_name(.)), session_id: $agents[.].session_id,
       ev: (about(.) + {session: $agents[.].session_id})}))
  as $revive

# --- Names Herdr lost: give each listed agent back its session's name, unless
# another agent took it meanwhile.
| ($snap.agents // [] | map(.name // empty)) as $taken
| ($snap.agents // [] | map(
    select(.name == null)
    | $live[.pane_id] as $v
    | select($v.name != null and .agent_session.value == $v.session_id)
    | select($v.name as $n | $taken | index($n) | not)
    | {pane_id, name: $v.name, session_id: $v.session_id, ev: (about(.pane_id) + {session: $v.session_id})}
  )) as $rename

# --- Usage limits: Claude often never continues by itself once its limit
# resets. Read each idle episode's pane once for a limit stop; once the limit
# has reset, nudge it once per episode, and never twice within limit_retry.
# An episode is read on its first tick, so it stopped about now.
| ($live | to_entries
   | map(select(.value.status == "idle" or .value.status == "done")
         | .value.episode = "\(.value.state_seq):\(.value.status_since)"))
  as $idle
| ($idle | map(select(.value.limit.episode != .value.episode))) as $limit_check
| ($limit_check | map(select(.key | seen("limit_reset"))
    | .value.limit = {episode: .value.episode, since: $now, reset_at: $obs[.key].limit_reset}))
  as $limit_read
| ($idle | map(.key as $k | ($limit_read | map(select(.key == $k)) | first) // .)) as $idle
| ($idle | map(
    .value as $v
    | select($v.limit.episode == $v.episode and $v.limit.reset_at != null
             and ($v.limit.resumed | not)
             and $now >= $v.limit.reset_at + $cfg.limit_grace
             and $now >= ($v.limit_resumed_at // 0) + $cfg.limit_retry)
    | select($v.workspace_label | corps_of_label | IN($over[]) | not)
    | {pane_id: .key, name: ($v.name // fallback_name(.key)), reset_at: $v.limit.reset_at, ev: about(.key)}))
  as $resume

# --- Panes to read before the decisions above can be made.
| ([$exit_check[] | select(seen("exited") | not)
    | {pane_id: ., session_id: $prev_agents[.].session_id,
       want: (if $due | index(.) then ["exited", "at_shell"] else ["exited"] end)}]
   + [$limit_check[] | select(.key | seen("limit_reset") | not)
      | {pane_id: .key, session_id: .value.session_id, want: ["limit_reset"]}]) as $look

# A Corps newly at its allowance this week: logged and spoken once.
| ($budget | map(select(.ratio >= 1)
    | select($prev_escalated["budget_block:\(.name):\($week)"] == null)
    | {key: "budget_block:\(.name):\($week)", corps: .name,
       text: "\(.name) used \(.used | . * 10 | round / 10)% of the week, over its \(.allowance)% budget"})) as $block

| ([$exited[] | about(.) + {session: $prev_agents[.].session_id}
     | {kind: "forgot", msg: "forgot \(.name) in \(.pane): user exited it (session \(.session))"} + .]
   + [$reviving[] | select($obs[.].at_shell | not) | about(.)
      | {kind: "revive_skipped", msg: "revive skipped: pane \(.pane) is not at a shell prompt"} + .]
   + [$limit_read[] | select(.value.limit.reset_at != null) | about(.key) + {until: $obs[.key].limit_reset}
      | {kind: "limit_stop", msg: "usage limit: \(.name) in \(.pane) stopped until \(.until | strflocaltime("%F %R %Z"))"} + .]
   + [$block[] | {kind: "budget_exhausted", corps: .corps,
       msg: "\(.text): holding Revives, usage-limit resumes and ask delivery for it and its Divisions until the week resets"}])
  as $decided

# --- Each Corps's Main Agent: a live Agent in the Corps's own workspace.
| ($agents | to_entries
   | map(select(.value.status != "gone"
                and (.value.workspace_label | is_division($divisions) | not)))
   | group_by(.value.workspace_label) | map({key: .[0].value.workspace_label, value: .[0].key})
   | from_entries) as $main_of
| ($main_of | with_entries(.key |= corps_name)) as $main_named

# --- Warning level reached: the Corps's Main Agent hears (Berthier when it has none).
| ($budget | map(select(.ratio >= ($cfg.budget_warn // 80) / 100 and .ratio < 1)
    | $main_named[.name] as $to
    | {key: "budget_warn:\(.name):\($week)", pane_id: (if $to == $berthier then $berthier else null end), to: $to,
       head: "[berthier-loop] Weekly budget:",
       text: "\(.name) has used \(.used | . * 10 | round / 10)% of the week, \(.ratio * 100 | floor)% of its \(.allowance)% budget: wind down low-value work, since at 100% its Revives, usage-limit resumes and ask delivery are held",
       ev: {why: "budget_warn", reason: "\(.ratio * 100 | floor)% of weekly budget", corps: .name, pct: (.ratio * 100 | floor)}})) as $warn

# --- Asks: deliver each to its addressee's Agent once it is ready for input,
# at most one per Agent per tick, oldest first.
| ($agents | to_entries | map(select(.value.status != "gone") | {
    pane_id: .key, status: .value.status,
    corps: (if .key == $berthier then "berthier" else .value.workspace_label | corps_of_label end),
    name: (if .key == $berthier then "berthier" else (.value.workspace_label | corps_name) end)
  })) as $addressable
| (reduce ($inbox | sort_by(.at))[] as $m ({used: [], deliver: []};
    . as $acc
    | ($m.to | corps_name) as $to
    | ([$addressable[] | select(.name == $to and (.status == "idle" or .status == "done"))
        | select(.corps | IN($over[]) | not)
        | select(.pane_id as $p | $acc.used | any(. == $p) | not)] | first) as $o
    | if $o == null then . else
        .used += [$o.pane_id]
        | .deliver += [{id: $m.id, pane_id: $o.pane_id, from: $m.from, to: $to,
            ev: {pane: $o.pane_id, from: $m.from, to: $to, ask: $m.id},
            oneway: ($m.kind == "notify"),
            text: (if $m.kind == "notify" then "[berthier notify] from \($m.from): \($m.text)"
                   else "[berthier ask] from \($m.from): \($m.text) (Reply or report back with: berthier ask \($m.from) \"...\")" end)}]
      end
  ) | .deliver) as $deliver
| ((($prev.ask_log // []) | map(select(.at > $now - 3600)))
   + ($deliver | map(select(.oneway | not) | {from, to, at: $now}))) as $ask_log

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
        {why: "revive_failed", reason: "revive failed \($v.revive_attempts)x", also: ", session \($v.session_id)"}
      elif $v.status == "blocked" and $age >= $cfg.blocked_after then
        {why: "blocked", reason: "blocked \($age / 60 | floor)m on an approval or question"}
      elif $v.status == "working" and $age >= $cfg.long_after then
        {why: "long", reason: "working \($age / 60 | floor)m without stopping"}
      # A spawned Division that stops may be done: its Corps reviews it.
      elif ($divisions[$v.workspace_label | corps_name].worktree // false)
           and ($v.status == "idle" or $v.status == "done")
           and $v.prev_status == "working" and $age >= $cfg.division_settle then
        {why: "division_stopped", reason: "division agent stopped working (finished or stalled)"}
      else empty end
    | {key: "\(.why):\($pane):\($v.status_since)", pane_id: $pane, name: $v.name, to: $to,
       text: "\(.reason): \($who)\(.also // "")", ev: (about($pane) + {why, reason})}
  )
  + ($inbox | map(
      select(.kind != "notify" and ($now - .at) >= $cfg.ask_stuck)
      | select(.id as $id | $deliver | any(.id == $id) | not)
      | "undelivered \(($now - .at) / 60 | floor)m" as $reason
      | {key: "ask_stuck:\(.id)", pane_id: null,
         text: "ask from \(.from) to \(.to) \($reason): \(.text)",
         ev: {why: "ask_stuck", reason: $reason, ask: .id, ask_from: .from, ask_to: (.to | corps_name)}}))
  # Ping-pong: replies flipping direction again and again; one-way updates don't count,
  # nor a Corps commanding and reviewing its own Division.
  + ($ask_log
     | map(select(.from as $f | .to as $t
         | [[$f, $t], [$t, $f]] | any(($divisions[.[0]].corps // null | corps_name) == .[1]) | not))
     | map(. + {pair: ([.from, .to] | sort)}) | group_by(.pair)
     | map(sort_by(.at) as $l
       | ([range(1; $l | length) | select($l[.].from != $l[. - 1].from)] | length) as $flips
       | select($flips >= $cfg.pingpong_max)
       | "\($flips) back-and-forths in the last hour" as $reason
       | {key: "pingpong:\($l[0].pair | join(" and ")):\($now / 3600 | floor)", pane_id: null,
          text: "asks bouncing between \($l[0].pair | join(" and ")): \($reason)",
          ev: {why: "pingpong", reason: $reason, ask_from: $l[0].pair[0], ask_to: $l[0].pair[1]}}))
  + $warn
  | map(select($prev_escalated[.key] == null)
        | .ev.to = (if .to then $agents[.to].workspace_label | corps_name else "berthier" end))) as $events

# --- Spoken alerts: the user is needed. Faster than escalation, because they
# skip Berthier's judgment; deduplicated per episode like escalations.
| ($agents | to_entries | map(
    .key as $pane | .value as $v
    | ($now - ($v.status_since // $now)) as $age
    | ($divisions[$v.workspace_label | corps_name].corps // null) as $parent
    | ($v.workspace_label // $v.workspace_id | spoken
       + (if $parent then " in \($parent | spoken)" else "" end)) as $where
    | if $v.status == "blocked" and $age >= $cfg.speak_after then
        {why: "blocked", text: "\($where) is waiting on you"}
      elif $v.status == "gone" and ($v.revive_attempts // 0) >= $cfg.revive_max then
        {why: "revive_failed", text: "\($where) crashed and could not be resumed"}
      else empty end
    | {key: "speak_\(.why):\($pane):\($v.status_since)", text, ev: (about($pane) + {why, to: "you"})}
  )
  + ($warn | map({key: "speak_\(.key)", text: "\(.ev.corps | spoken) has used \(.ev.pct) percent of its weekly budget",
                  ev: {corps: .ev.corps, why: "budget_warn", to: "you"}}))
  + ($block | map({key: "speak_\(.key)", text: "\(.corps | spoken) is over its weekly budget",
                   ev: {corps: .corps, why: "budget_exhausted", to: "you"}}))
  | map(select($prev_escalated[.key] == null))) as $speak

# Berthier cannot escalate to itself: its own events go to the user instead.
| ($events | map(select(.pane_id == null or .pane_id != $berthier))) as $escalate
| ($events | map(select(.pane_id != null and .pane_id == $berthier) | .ev.to = "you")) as $notify

| ($prev_escalated
   | with_entries(select(.value > ($now - $cfg.escalation_ttl)))
   | . + ($events + $speak + $block | map({key: .key, value: $now}) | from_entries)) as $escalated

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

# --- The Grand Army State: what was decided above, recorded.
| ($agents
   | reduce $limit_read[] as $e (.; .[$e.key].limit = $e.value.limit)
   | reduce $reviving[] as $p (.; .[$p].revive_attempts += 1 | .[$p].last_revive_at = $now)
   | reduce $resume[] as $r (.; .[$r.pane_id].limit.resumed = true | .[$r.pane_id].limit_resumed_at = $now))
  as $recorded

| {
    state: {
      version: 1,
      updated_at: $now,
      berthier: $berthier,
      corps: $corps,
      berthier_divisions: $berthier_divisions,
      order: [$workspaces[].workspace_id],
      agents: $recorded,
      escalated: $escalated,
      ask_log: $ask_log
    },
    look: $look,
    events: $decided,
    revive: $revive,
    rename: $rename,
    resume: $resume,
    escalate: $escalate,
    notify: $notify,
    speak: $speak,
    deliver: $deliver
  }
