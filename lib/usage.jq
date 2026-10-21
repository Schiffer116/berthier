# Weekly quota attribution, pure. `berthier sample` appends one row per change
# of a session's status line ({ts, session, cwd, cost, week_pct, resets_at});
# this folds new rows into the usage record (state/usage.json), so each Corps
# knows how much of the account's seven-day quota it used.
#
# Each rise in week_pct is split across the sessions whose cost rose since the
# last rise, weighted by that rise in cost, and credited to the Corps their cwd
# belongs to (a Division counts toward its Corps; no Corps: "other"). A rise
# no session's cost explains goes to "other". The first row ever seen is only a
# baseline: what the account used before sampling started belongs to no one.
#
# Usage record: {resets_at, week_pct, [five_pct, five_resets_at], by_corps: {corps: pct}, acc: {session: Δcost
# since the last rise}, sessions: {session: {cost, corps, ts}}, offset: ledger bytes folded}
include "names";

# The Corps a Division counts toward, else the Corps itself.
def owner($divisions): corps_name as $n | ($divisions[$n].corps // $n) | corps_name;

# [[path, corps]] from the Grand Army State: where each Corps's Agents and
# Divisions work. Longest path wins, so a Division's worktree beats its repo.
def path_map($state; $divisions):
  (($state.agents // {})[$state.berthier // ""].workspace_label // "berthier" | corps_name) as $b
  | [($state.agents // {})[] | select(.cwd != null and .cwd != "/") | [.cwd, (.workspace_label | owner($divisions))]]
    + [($state.corps // [])[] | (.name | corps_name) as $c | [.repo_root // empty, (.divisions[]?.path // empty)][] | [., $c]]
    + [($state.berthier_divisions // [])[] | [.path // empty, $b]];

def corps_of($cwd; $map):
  [$map[] | . as [$path] | select($cwd != null and ($cwd == $path or ($cwd | startswith($path + "/"))))]
  | max_by(.[0] | length) | .[1];

# A row from another window: resets_at moved, or (no resets_at) the quota fell.
def new_window($r):
  .week_pct != null
  and (if $r.resets_at != null and .resets_at != null then (($r.resets_at - .resets_at) | fabs) > 3600
       else $r.week_pct < .week_pct end);

def rise($amt):
  ((.acc // {}) | to_entries) as $a
  | ([$a[].value] | add // 0) as $total
  | (if $total > 0
     then reduce $a[] as $e (.; .by_corps[.sessions[$e.key].corps] += $amt * $e.value / $total)
     else .by_corps.other += $amt end)
  | .acc = {};

def step($r; $map):
  (if new_window($r) then .week_pct = 0 | .by_corps = {} | .acc = {} else . end)
  | .sessions[$r.session] as $s
  | (corps_of($r.cwd; $map) // $s.corps // "other") as $corps
  | ($s | if . == null then 0 else [$r.cost - .cost, 0] | max end) as $d
  | .sessions[$r.session] = {cost: $r.cost, corps: $corps, ts: $r.ts}
  | (if $d > 0 then .acc[$r.session] += $d else . end)
  | (if .week_pct == null then .week_pct = $r.week_pct
     elif $r.week_pct > .week_pct then rise($r.week_pct - .week_pct) | .week_pct = $r.week_pct
     else . end)
  | .resets_at = ($r.resets_at // .resets_at)
  | (if ($r.five_pct | type) == "number" then .five_pct = $r.five_pct | .five_resets_at = $r.five_resets_at else . end);

# $usage (or {}) folded over $rows, in ledger order; sessions idle for two weeks forgotten.
def fold($rows; $map; $offset):
  (.acc //= {}) | (.by_corps //= {}) | (.sessions //= {})
  | reduce $rows[] as $r (.; step($r; $map))
  | (([$rows[].ts] | max) // 0) as $latest
  | .sessions |= with_entries(select(.value.ts > $latest - 1209600))
  | .offset = $offset;

# Each Corps with usage or a budget this week, hungriest first: {name, used,
# allowance, ratio} (% of the week; allowance and ratio null without a budget).
# A record whose week has ended counts for nothing until the next sample.
def usage_view($usage; $budgets; $now):
  (($usage.resets_at // $now + 1) > $now) as $live
  | (if $live then $usage.by_corps // {} else {} end) as $used
  | ($budgets | with_entries(.key |= corps_name)) as $b
  | {resets_at: $usage.resets_at,
     week_pct: (if $live then $usage.week_pct else null end),
     five_hour: (if ($usage.five_pct | type) == "number" and (($usage.five_resets_at // $now + 1) > $now)
                 then {pct: $usage.five_pct, resets_at: $usage.five_resets_at} else null end),
     corps: ([($used | keys[]), ($b | keys[])] | unique | map(
       . as $n | ($used[$n] // 0) as $u | ($b[$n] // null) as $a
       | {name: $n, used: $u, allowance: $a, ratio: (if $a then $u / $a else null end)})
       | sort_by(-.used, .name))};
