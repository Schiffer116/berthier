# The Army view model: Army State + Agents' reports + inbox + loop.log, joined
# for the bar, the desktop widget and the dashboard. Pure.
#
# Inputs (--argjson): $state (state.json or {}), $reports {session_id: {line, at}},
#   $inbox [{id, from, to, text, at}], $log [recent lines of loop.log],
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

# "2026-10-17T13:02:57+07:00" -> epoch seconds (jq's mktime ignores offsets).
def iso_epoch:
  capture("^(?<d>.{19})(?<s>[+-])(?<h>\\d\\d):(?<m>\\d\\d)$")
  | (.d + "Z" | fromdateiso8601) - ((if .s == "+" then 1 else -1 end) * ((.h | tonumber) * 3600 + (.m | tonumber) * 60));

# "2026-10-17T13:02:57+07:00 delivered ask ..." -> {at (epoch), text, tag}
def log_entry:
  capture("^(?<ts>\\S+) (?<text>.*)$")?
  | . + {at: (.ts | iso_epoch)}
  | .tag = (.text | if test("^loop ") then "loop"
                    elif test("^revive") then "revive"
                    elif test("^forgot ") then "forget"
                    elif test("^(spoke|speak)") then "speak"
                    elif test("^(escalated|notified|.* unreachable)") then "escal"
                    elif test("^(delivered ask|ask )") then "ask"
                    elif test("^(adopted|relabelled)") then "army"
                    elif test("^(error|tick failed)") then "error"
                    else "other" end)
  | del(.ts);

# An escalation line's subject, split off its reason, as the dashboard shows it:
# {who: "learning wR:p1", why: "blocked 3m on an approval or question"}.
def escalation_who:
  (capture("^(?<why>.*): agent '[^']*' in (?<w>.*) \\(pane (?<p>[^)]+)\\)$") | {who: "\(.w | corps_name) \(.p)", why})
  // (capture("^asks bouncing between (?<a>\\S+) and (?<b>\\S+): (?<why>.*)$") | {who: "\(.a) ⇄ \(.b)", why})
  // (capture("^ask from (?<a>\\S+) to (?<b>\\S+) (?<why>undelivered .*)$") | {who: "\(.a) → \(.b)", why})
  // {who: null, why: .};

($state.agents // {}) as $agents
| [($state.corps // [])[] | unit_view
   + {divisions: [.divisions[]? | unit_view + {worktree: (.worktree // false)}]}] as $corps
| [($state.berthier_divisions // [])[] | unit_view + {worktree: (.worktree // false)}] as $bdivs
| [$corps[] | ., .divisions[] | .agents[]]
  + (if $state.berthier then [agent_view($state.berthier)] else [] end)
  + [$bdivs[] | .agents[]] as $all
| [$log[] | log_entry] as $entries
| {
    available: $available,
    updated_at: ($state.updated_at // null),
    loop: {interval: $interval, revive_max: $revive_max,
           started_at: ([$entries[] | select(.text | test("^loop started"))] | last | .at // null)},
    counts: ([["working","blocked","done","idle","unknown","gone"][] as $k
              | {($k): ([$agents[].status] | map(select(. == $k)) | length)}] | add),
    needs_you: [$all[] | select(.needs_you)] | sort_by(.status_since // 0, .who),
    berthier: (if $state.berthier then agent_view($state.berthier) else null end),
    corps: $corps,
    berthier_divisions: $bdivs,
    asks: ($inbox | sort_by(.at)),
    ask_log: (($state.ask_log // []) | sort_by(.at) | reverse),
    escalations: [$entries[]
      | select(.at >= $now - 7 * 86400) | .at as $at
      | (.text | (capture("^escalated (?<kind>\\S+) to (?<to>\\S+): (?<why>.*)$")
                  | . + (.why | escalation_who)
                  | .to = (if $agents[.to] then agent_view(.to).who else .to end))
                 // (capture("^spoke: (?<why>.*)$")
                     | {kind: "speak", to: "you", why,
                        who: (.why | capture("^(?<w>.+?) (is waiting|crashed)").w)})?)
      | {at: $at, kind, to, who, why}
      ] | reverse,
    activity: ($entries[-200:] | reverse)
  }
