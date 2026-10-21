#!/usr/bin/env bash
# Unit tests for usage_limit_reset: a pane's tail in, the limit's reset time out.
set -euo pipefail
cd "$(dirname "$0")/.."

home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
# shellcheck source=lib/common.sh
BERTHIER_HOME=$home source lib/common.sh
set +e

pass=0 fail=0
check() { # name actual expected
  if [[ $2 == "$3" ]]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$1" "$2" "$3"; fi
}
at() { TZ=${2:-Asia/Ho_Chi_Minh} date -d "$1" +%s; }
reset() { # since; tail on stdin -> reset as local time, or nothing
  local r
  r=$(TZ=Asia/Ho_Chi_Minh usage_limit_reset "$1")
  [[ -z $r ]] || TZ=Asia/Ho_Chi_Minh date -d "@$r" '+%F %R'
}
box=$'\n                                            ✔ Update installed · /restart to apply
                                                                ◐ medium · /effort
──────────────────────────────────────────────────
❯
──────────────────────────────────────────────────
  5h: 15% | 7d: 39% | Ctx: 25% | Cost: $1.97
  ⏵⏵ auto mode on (shift+tab to cycle) · PR #16'

# The real stop of 2026-10-20 (skipli dark-mode, w13:p1).
stop="❯ [berthier ask] from skipli: make dark mode match Cloudscape's
  ⎿  You've hit your session limit · resets 11pm (Asia/Ho_Chi_Minh)
     /upgrade or /usage-credits to finish what you’re working on.

● Usage limit reached · continuing automatically at 11pm · esc to cancel

✻ Worked for 1s · done Tuesday 9:30 PM
$box"
check "session limit" "$(reset "$(at '2026-10-20 21:30')" <<<"$stop")" '2026-10-20 23:00'
check "late since lands on the same reset" "$(reset "$(at '2026-10-20 23:40')" <<<"$stop")" '2026-10-20 23:00'
check "reset past midnight" "$(reset "$(at '2026-10-20 21:30')" <<<"${stop//11pm/1:30am}")" '2026-10-21 01:30'
check "reset in its own time zone" "$(reset "$(at '2026-10-20 21:30')" <<<"${stop//Asia\/Ho_Chi_Minh/Europe\/London}")" '2026-10-21 05:00'

only="● Done the first half.

  ⎿  You've hit your session limit · resets 12:50pm (Asia/Ho_Chi_Minh)
     /upgrade or /usage-credits to finish what you’re working on.
$box"
check "session limit line alone" "$(reset "$(at '2026-10-20 09:00')" <<<"$only")" '2026-10-20 12:50'

check "no time zone means local" "$(reset "$(at '2026-10-20 21:30')" <<<"● Usage limit reached · continuing automatically at 11:30pm · esc to cancel
$box")" '2026-10-20 23:30'
check "weekly limit with a date" "$(reset "$(at '2026-10-20 21:30')" <<<"  ⎿  You've hit your weekly limit · resets Oct 24, 9am (Asia/Ho_Chi_Minh)
$box")" '2026-10-24 09:00'
check "unreadable reset waits 5h" "$(reset "$(at '2026-10-20 21:30')" <<<"  ⎿  You've hit your session limit · resets soon
$box")" '2026-10-21 02:30'

check "work after the stop is not a stop" "$(reset "$(at '2026-10-20 23:10')" <<<"${stop/$box/}
❯ The usage limit has reset. Continue exactly where you left off.

● All done.

✻ Cooked for 3m · done 11:14 PM
$box")" ''
check "blank padding above the input box" "$(reset "$(at '2026-10-20 21:30')" <<<"${stop/$box/$(printf '\n%.0s' {1..30})$box}")" '2026-10-20 23:00'
check "plain idle" "$(reset "$(at '2026-10-20 09:00')" <<<"● Pushed PR #18.
$box")" ''
check "limit typed in the input box" "$(reset "$(at '2026-10-20 09:00')" <<<"● Pushed.
──────────────────────────────────────────────────
❯ what if you hit your session limit · resets 11pm?
──────────────────────────────────────────────────")" ''

printf '%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
