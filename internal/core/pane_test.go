package core

import (
	"strings"
	"testing"
	"time"
)

func at(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, hcm)
	if err != nil {
		t.Fatal(err)
	}
	return tm.Unix()
}

var hcm, _ = time.LoadLocation("Asia/Ho_Chi_Minh")

func reset(t *testing.T, since int64, screen string) string {
	t.Helper()
	r, ok := UsageLimitReset(screen, since, hcm)
	if !ok {
		return ""
	}
	return time.Unix(r, 0).In(hcm).Format("2006-01-02 15:04")
}

const box = `
                                            ✔ Update installed · /restart to apply
                                                                ◐ medium · /effort
──────────────────────────────────────────────────
❯
──────────────────────────────────────────────────
  5h: 15% | 7d: 39% | Ctx: 25% | Cost: $1.97
  ⏵⏵ auto mode on (shift+tab to cycle) · PR #16`

func TestUsageLimitReset(t *testing.T) {
	// The real stop of 2026-10-20 (skipli dark-mode, w13:p1).
	stop := "❯ [berthier ask] from skipli: make dark mode match Cloudscape's\n" +
		"  ⎿  You've hit your session limit · resets 11pm (Asia/Ho_Chi_Minh)\n" +
		"     /upgrade or /usage-credits to finish what you’re working on.\n\n" +
		"● Usage limit reached · continuing automatically at 11pm · esc to cancel\n\n" +
		"✻ Worked for 1s · done Tuesday 9:30 PM\n" + box
	cases := []struct{ name, since, screen, want string }{
		{"session limit", "2026-10-20 21:30", stop, "2026-10-20 23:00"},
		{"late since lands on the same reset", "2026-10-20 23:40", stop, "2026-10-20 23:00"},
		{"reset past midnight", "2026-10-20 21:30", strings.ReplaceAll(stop, "11pm", "1:30am"), "2026-10-21 01:30"},
		{"reset in its own time zone", "2026-10-20 21:30", strings.ReplaceAll(stop, "Asia/Ho_Chi_Minh", "Europe/London"), "2026-10-21 05:00"},
		{"session limit line alone", "2026-10-20 09:00", "● Done the first half.\n\n  ⎿  You've hit your session limit · resets 12:50pm (Asia/Ho_Chi_Minh)\n     /upgrade or /usage-credits to finish what you’re working on.\n" + box, "2026-10-20 12:50"},
		{"no time zone means local", "2026-10-20 21:30", "● Usage limit reached · continuing automatically at 11:30pm · esc to cancel\n" + box, "2026-10-20 23:30"},
		{"weekly limit with a date", "2026-10-20 21:30", "  ⎿  You've hit your weekly limit · resets Oct 24, 9am (Asia/Ho_Chi_Minh)\n" + box, "2026-10-24 09:00"},
		{"unreadable reset waits 5h", "2026-10-20 21:30", "  ⎿  You've hit your session limit · resets soon\n" + box, "2026-10-21 02:30"},
		{"work after the stop is not a stop", "2026-10-20 23:10", strings.Replace(stop, box, "", 1) + "\n❯ The usage limit has reset. Continue exactly where you left off.\n\n● All done.\n\n✻ Cooked for 3m · done 11:14 PM\n" + box, ""},
		{"blank padding above the input box", "2026-10-20 21:30", strings.Replace(stop, box, strings.Repeat("\n", 30)+box, 1), "2026-10-20 23:00"},
		{"plain idle", "2026-10-20 09:00", "● Pushed PR #18.\n" + box, ""},
		{"limit typed in the input box", "2026-10-20 09:00", "● Pushed.\n──────────────────────────────────────────────────\n❯ what if you hit your session limit · resets 11pm?\n──────────────────────────────────────────────────", ""},
	}
	for _, c := range cases {
		if got := reset(t, at(t, c.since), c.screen); got != c.want {
			t.Errorf("%s\n  got:  %q\n  want: %q", c.name, got, c.want)
		}
	}
}

func TestExitedDeliberately(t *testing.T) {
	const sid = "0b6f2c1e-6a1d-4c55-9d1f-3f1e2a7c9b10"
	quit := "● Pushed PR #18.\n\n" + box + "\n\nResume this session with:\nclaude --resume " + sid + "\n\n~/r/skipli main ❯"
	cases := []struct {
		name, tail string
		want       bool
	}{
		{"clean exit", quit, true},
		{"clean exit, blank lines below", quit + strings.Repeat("\n", 15), true},
		{"another session's hint", strings.ReplaceAll(quit, sid, "11111111-2222-3333-4444-555555555555"), false},
		{"killed process", "● Pushed PR #18.\n" + box + "\nzsh: killed     claude\n~/r/skipli main ❯", false},
		{"old hint scrolled up", quit + "\n❯ claude --resume " + sid + "\n● Back again.\n" + box + "\nzsh: killed     claude\n~/r/skipli main ❯", false},
	}
	for _, c := range cases {
		if got := ExitedDeliberately(c.tail, sid); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestInputHolds(t *testing.T) {
	rule := strings.Repeat("─", 50)
	ask := "[berthier ask] from ui-tweaks: PR #20 (ui-tweaks -> dev): board header Edit board pencil (owner only, offline Solo only; save reloads header+favs, delete -> /boards). Screenshots: /tmp/a.png /tmp/b.png /tmp/c.png /tmp/d.png /tmp/e.png. Non-owner case not E2E'd (needs 2nd user), same owner rule as list."
	draft := "● Done.\n                                                                         ctrl+g to edit in Nvim\n" + rule +
		"\n❯ [Image #4] [Image #5] [Image #6] [Image #7][berthier ask] from ui-tweaks: PR #20 (ui-tweaks -> dev): board header Edit board pencil (owner only, offline Solo only; save\n  reloads header+favs, delete ->\n  /boards). Screenshots: /tmp/e.png. Non-owner case not\n  E2E'd (needs 2nd user), same owner rule as list.\n" +
		rule + "\n  5h: 87% | 7d: 48% | Cost: $0.00"
	cases := []struct {
		name, text, screen string
		want               bool
	}{
		{"image draft held", ask, draft, true},
		{"text draft held", "Reply with just: pong, then stop", rule + "\n❯ Reply with just:\n  pong, then stop\n" + rule, true},
		{"long paste held", "whatever", rule + "\n❯ [Pasted text #1 +40 lines]\n" + rule, true},
		{"empty box", ask, rule + "\n❯\n" + rule, false},
		{"idle suggestion is not ours", ask, rule + "\n❯ commit this\n" + rule, false},
		{"no box", ask, "● working", false},
	}
	for _, c := range cases {
		if got := InputHolds(c.screen, c.text); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestSlugAndNames(t *testing.T) {
	for in, want := range map[string]string{"Dark Mode": "dark-mode", "demo-dark-mode": "demo-dark-mode", "9 Lives!": "lives", ".dot files": "dot-files",
		"abcdefghijklmnopqrstuvwxyzabcdefghij": "abcdefghijklmnopqrstuvwxyzabcdef"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if CorpsName(".Dotfiles") != "dotfiles" || Spoken(".dark-mode_x") != "dark mode x" || FallbackName("W1:P1") != "a-w1-p1" {
		t.Error("names")
	}
}
