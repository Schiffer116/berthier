# The Army view model: Fleet State + Agents' reports + inbox + activity, joined
# for the bar, the desktop widget and the dashboard. Pure.
#
# Inputs (--argjson): $state (state.json or {}), $reports {session_id: {line, at}},
#   $inbox [{id, from, to, text, at}], $log [lines of loop.log], $available, $now
include "names";

def agent_view($pane):
  ($state.agents[$pane] // {}) as $a
  | ($reports[$a.session_id // ""] // null) as $r
  | ($r.line // null) as $line
  | (($line // "") | test("^needs you:"; "i")) as $needs
  | {pane_id: $pane, name: $a.name, status: $a.status, status_since: $a.status_since,
     who: (if $pane == $state.berthier then "berthier" else ($a.workspace_label | corps_name) end),
     needs_you: ($needs or $a.status == "blocked"),
     report: (if $line == null then null else ($line | sub("^needs you: *"; ""; "i")) end),
     report_at: ($r.at // null)};

def unit_view: {name: (.name | corps_name), workspace_id, agents: [.agents[] | agent_view(.)]};

($state.agents // {}) as $agents
| [($state.corps // [])[] | unit_view
   + {divisions: [.divisions[]? | unit_view], tasks: [.tasks[]? | unit_view]}] as $corps
| [$corps[] | ., .divisions[], .tasks[] | .agents[]]
  + (if $state.berthier then [agent_view($state.berthier)] else [] end) as $all
| {
    available: $available,
    updated_at: ($state.updated_at // null),
    counts: ([["working","blocked","done","idle","unknown","gone"][] as $k
              | {($k): ([$agents[].status] | map(select(. == $k)) | length)}] | add),
    needs_you: [$all[] | select(.needs_you)] | sort_by(.status_since // 0, .who),
    berthier: (if $state.berthier then agent_view($state.berthier) else null end),
    corps: $corps,
    asks: ($inbox | sort_by(.at)),
    activity: [$log[-40:][] | capture("^(?<at>\\S+) (?<text>.*)$")? ] | reverse
  }
