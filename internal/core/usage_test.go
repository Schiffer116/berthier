package core

import (
	"math"
	"testing"
)

func row(session string, ts, week float64, resets ...float64) Row {
	cwd := "/x"
	r := Row{Ts: ts, Session: session, Cwd: &cwd, WeekPct: week, ResetsAt: ptr(1800000000.0)}
	if len(resets) > 0 {
		r.ResetsAt = ptr(resets[0])
	}
	return r
}

func by(u Usage) map[string]float64 {
	m := map[string]float64{}
	for k, v := range u.ByCorps {
		m[k] = math.Round(v*1000) / 1000
	}
	return m
}

func TestFoldAttribution(t *testing.T) {
	// Transcript Spend by time: skipli, a Division of skipli counted as skipli
	// by the Profile already, dotfiles.
	spend := []SpendAt{{5, "skipli", 3}, {15, "skipli", 1}, {16, "dotfiles", 1}, {25, "skipli", 1}, {35, "dotfiles", 2}}
	u := Usage{}.Fold([]Row{
		row("a", 1, 40),  // baseline
		row("a", 10, 41), // +1: only skipli spent
		row("b", 20, 43), // +2: skipli 1, dotfiles 1
		row("a", 30, 44), // +1: skipli
		row("c", 40, 46), // +2: dotfiles
	}, spend, 0)
	check(t, "split by spend", by(u), `{"skipli":3,"dotfiles":3}`)
	check(t, "week tracked", []any{u.WeekPct, u.ResetsAt, u.Source, u.LastRise}, `[46,1800000000,"transcripts",41]`)

	spend = append(spend, SpendAt{45, "skipli", 2}, SpendAt{46, "dotfiles", 1.5})
	u = u.Fold([]Row{row("a", 47, 48)}, spend, 0)
	check(t, "weighted by spend", by(u), `{"skipli":4.143,"dotfiles":3.857}`)

	u = u.Fold([]Row{row("z", 60, 52)}, spend, 0)
	check(t, "a rise no spend explains goes to other", by(u)["other"], `4`)

	// Spend at the row's own second counts (rows carry whole seconds).
	spend = append(spend, SpendAt{70.4, "dotfiles", 1})
	u = u.Fold([]Row{row("a", 70, 53)}, spend, 0)
	check(t, "spend within the row's second", by(u)["dotfiles"], `4.857`)

	// A new window starts over.
	spend = append(spend, SpendAt{75, "skipli", 1})
	u = u.Fold([]Row{row("a", 80, 3, 1900000000)}, spend, 0)
	check(t, "new window starts from 0", []any{u.ResetsAt, u.WeekPct, by(u)}, `[1900000000,3,{"skipli":3}]`)
	u = u.Fold([]Row{row("a", 90, 5, 1900000000)}, append(spend, SpendAt{85, "skipli", 1}), 0)
	check(t, "next rise of the new window", by(u), `{"skipli":5}`)
}

func TestFoldBaselineOnly(t *testing.T) {
	r := row("r1", 1, 73)
	r.ResetsAt = nil
	u := Usage{}.Fold([]Row{r}, nil, 0)
	check(t, "baseline-only shows", []any{u.View(nil, 1).WeekPct, u.View(nil, 1).Corps}, `[73,[]]`)
	r2 := row("r1", 10, 74)
	r2.ResetsAt = nil
	check(t, "rise after a baseline-only record", by(u.Fold([]Row{r2}, []SpendAt{{5, "dotfiles", 1}}, 0)), `{"dotfiles":1}`)
}

func TestFoldFiveHour(t *testing.T) {
	r := row("f1", 1, 5)
	r.FivePct, r.FiveResetsAt = ptr(31.0), ptr(1799990000.0)
	u := Usage{}.Fold([]Row{r}, nil, 0)
	check(t, "five-hour in view", u.View(nil, 1).FiveHour, `{"pct":31,"resets_at":1799990000}`)
	// Old rows without it still fold; their week_pct is ignored.
	u = u.Fold([]Row{row("f1", 2, 6)}, nil, 0)
	check(t, "row without five-hour folds, its week_pct ignored", []any{u.WeekPct, u.FivePct}, `[5,31]`)
}

func TestFoldManualReset(t *testing.T) {
	// A manual reset keeps resets_at but drops week_pct; stale rows without
	// five_pct (a session's first render) neither reset nor fake a rise.
	mk := func(ts float64, s string, week float64, five ...float64) Row {
		r := row(s, ts, week, 1792900800)
		if len(five) > 0 {
			r.FivePct, r.FiveResetsAt = ptr(five[0]), ptr(1792700000.0)
		}
		return r
	}
	u := Usage{}.Fold([]Row{mk(1, "s1", 80, 10), mk(2, "s1", 81, 11), mk(3, "s2", 75), mk(4, "s1", 81, 12), mk(5, "s1", 92, 14), mk(6, "s3", 92),
		mk(7, "s1", 0, 1), mk(8, "s1", 1, 2)}, nil, 0)
	rounded := map[string]float64{}
	for k, v := range u.ByCorps {
		rounded[k] = math.Round(v)
	}
	check(t, "manual reset starts a new window", []any{u.WeekPct, rounded}, `[1,{"other":1}]`)
}

func TestFoldIsPure(t *testing.T) {
	u := Usage{}.Fold([]Row{row("a", 1, 40)}, nil, 0)
	before := js(t, u)
	_ = u.Fold([]Row{row("a", 3, 41)}, []SpendAt{{2, "x", 1}}, 0)
	check(t, "input record untouched", js(t, u), mustJSON(t, before))
}

func TestForgetsIdleSessions(t *testing.T) {
	u := Usage{}.Fold([]Row{row("old", 1, 1), row("new", 1209601+10, 1)}, nil, 0)
	if _, ok := u.Sessions["old"]; ok || len(u.Sessions) != 1 {
		t.Errorf("sessions: %v", u.Sessions)
	}
}

func TestUsageView(t *testing.T) {
	u := Usage{ResetsAt: ptr(9000.0), WeekPct: ptr(50.0), ByCorps: map[string]float64{"skipli": 3, "dotfiles": 0, "other": 3}}
	v := u.View(map[string]float64{"Skipli": 10, ".Notes": 5}, 1000)
	check(t, "view sorted hungriest first, budgets joined", v.Corps,
		`[{"name":"other","used":3,"allowance":null,"ratio":null},{"name":"skipli","used":3,"allowance":10,"ratio":0.3},{"name":"dotfiles","used":0,"allowance":null,"ratio":null},{"name":"notes","used":0,"allowance":5,"ratio":0}]`)
	v = u.View(nil, 9001)
	check(t, "ended week counts for nothing", []any{v.WeekPct, v.Corps}, `[null,[]]`)
}
