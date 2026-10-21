# The Army view model: Army State + Agents' reports + inbox + loop.log, joined
# for the bar, the desktop widget and the dashboard. Pure.
#
# Inputs (--argjson): $state (state.json or {}), $reports {session_id: {line, at}},
#   $inbox [{id, from, to, text, at}], $log [recent lines of loop.log, JSON Lines],
#   $available, $now, $interval (Main Loop seconds), $revive_max
include "names";

def agent_view($pane):
  ($state.agents[$pane] // {}) as $a
  | ($reports[$a.session_id // ""] // null) as $r
  | ($r.line // null) as $line
  | (($line // "") | test("^needs you:"; "i")) as $needs
  | {pane_id: $pane, name: $a.name, kind: $a.kind, tab_id: $a.tab_id,
     status: $a.status, status_since: $a.status_since, prev_status: $a.prev_status,
     session_id: $a.session_id, title: $a.title, cwd: $a.cwd,
     revive_attempts: ($a.revive_attempts // 0), last_revive_at: $a.last_revive_at,
     who: (if $pane == $state.berthier then "berthier" else ($a.workspace_label | corps_name) end),
     needs_you: ($needs or $a.status == "blocked"),
     report: (if $line == null then null else ($line | sub("^needs you: *"; ""; "i")) end),
     report_at: ($r.at // null)};

def unit_view: {name: (.name | corps_name), workspace_id, agents: [.agents[] | agent_view(.)]};

# A loop.log event ({ts, kind, msg, ...fields}; see `event` in lib/common.sh)
# with its Console category (tag) and display label.
def log_entry:
  .kind as $k
  | ({loop_started: "loop", loop_stopped: "loop", loop_recovered: "loop", loop_error: "error",
      revived: "revive", revive_failed: "revive", revive_skipped: "revive", forgot: "forget",
      spoke: "speak", speak_failed: "speak", escalated: "escal", notified: "escal", unreachable: "escal",
      delivered: "ask", delivery_failed: "ask", adopted: "army", brief_sent: "army", brief_failed: "army", renamed: "army", rename_failed: "army",
      limit_stop: "limit", resumed: "limit", resume_failed: "limit"}[$k] // "other") as $tag
  | . + {at: .ts, tag: $tag, text: .msg, label: (if $tag == "other" then "info" else $tag end)};

# An escalation's condition as the Console labels it.
def escalation_label:
  {revive_failed: "revive failed", division_stopped: "stopped", long: "long run", ask_stuck: "ask stuck"}[.] // .;

# Who an escalation is about, as the Console shows it: "dark-mode d:p1",
# "dotfiles ⇄ skipli" (asks bouncing), "skipli → notes" (an ask stuck).
def escalation_who:
  if .ask_from then "\(.ask_from) \(if .why == "pingpong" then "⇄" else "→" end) \(.ask_to)"
  elif .pane then "\(.corps) \(.pane)"
  else null end;

($state.agents // {}) as $agents
| [($state.corps // [])[] | unit_view
   + {divisions: [.divisions[]? | unit_view + {worktree: (.worktree // false)}]}] as $corps
| [($state.berthier_divisions // [])[] | unit_view + {worktree: (.worktree // false)}] as $bdivs
# Herdr's workspace order: Corps and Berthier as Herdr lists them, each
# unit's Divisions under it in the same order.
| ($state.order // [] | to_entries | map({key: .value, value: .key}) | from_entries) as $pos
| def herdr_order: sort_by($pos[.workspace_id // ""] // 1e9);
  ($corps | map(.divisions |= herdr_order)) as $corps
| ($bdivs | herdr_order) as $bdivs
| ($corps | map({kind: "corps"} + .)
   + (if $state.berthier then [{kind: "berthier", name: "berthier", workspace_id: $agents[$state.berthier].workspace_id,
                                agents: [agent_view($state.berthier)], divisions: $bdivs}] else [] end)
   | herdr_order) as $units
| [$corps[] | ., .divisions[] | .agents[]]
  + (if $state.berthier then [agent_view($state.berthier)] else [] end)
  + [$bdivs[] | .agents[]] as $all
# Lines that are not events (a stray prose line) are skipped.
| [$log[] | fromjson? | objects | log_entry] as $entries
| {
    available: $available,
    updated_at: ($state.updated_at // null),
    loop: {interval: $interval, revive_max: $revive_max,
           started_at: ([$entries[] | select(.kind == "loop_started")] | last | .at // null)},
    counts: ([["working","blocked","done","idle","unknown","gone"][] as $k
              | {($k): ([$agents[].status] | map(select(. == $k)) | length)}] | add),
    needs_you: [$all[] | select(.needs_you)] | sort_by(.status_since // 0, .who),
    berthier: (if $state.berthier then agent_view($state.berthier) else null end),
    corps: $corps,
    berthier_divisions: $bdivs,
    units: $units,
    asks: ($inbox | sort_by(.at)),
    ask_log: (($state.ask_log // []) | sort_by(.at) | reverse),
    escalations: [$entries[]
      | select(.at >= $now - 7 * 86400 and (.kind == "escalated" or .kind == "notified" or .kind == "spoke"))
      | if .kind == "spoke" then {at, kind: "speak", label: "speak", to: "you", who: .corps, why: (.msg | ltrimstr("spoke: "))}
        else {at, kind: .why, label: (.why | escalation_label), to, who: escalation_who, why: .reason} end
      ] | reverse,
    activity: ($entries[-200:] | reverse)
  }
