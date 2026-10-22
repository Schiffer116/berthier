package core

import (
	"math"
	"testing"
)

// Corps: skipli at /r/skipli with a Division at /h/dark-mode; dotfiles at /r/dot.
var usageMap = []PathCorps{{"/r/skipli", "skipli"}, {"/h/dark-mode", "skipli"}, {"/r/dot", "dotfiles"}, {"/r/berthier", "berthier"}}

func row(session, cwd string, cost, week float64, resets ...float64) Row {
	r := Row{Ts: 1, Session: session, Cwd: &cwd, Cost: cost, WeekPct: week, ResetsAt: ptr(1800000000.0)}
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

func fold(u Usage, rows ...Row) Usage { return u.Fold(rows, usageMap, 0) }

func TestFoldAttribution(t *testing.T) {
	u := fold(Usage{},
		row("a", "/r/skipli", 0, 40), row("d", "/h/dark-mode", 0, 40), row("c", "/r/dot", 0, 40),
		row("a", "/r/skipli", 3, 41),    // week +1, only a's cost rose: skipli
		row("d", "/h/dark-mode", 1, 41), // no rise yet
		row("c", "/r/dot", 1, 43),       // +2, d and c rose by 1 each: skipli (its Division) and dotfiles
		row("a", "/r/skipli", 4, 44),    // +1: skipli
		row("c", "/r/dot", 2, 46))       // +2: dotfiles
	check(t, "split by corps", by(u), `{"skipli":3,"dotfiles":3}`)
	check(t, "week tracked", []any{u.WeekPct, u.ResetsAt}, `[46,1800000000]`)

	u = fold(u, row("a", "/r/skipli", 6, 46), row("c", "/r/dot", 3, 46), row("c", "/r/dot", 3.5, 48)) // a +2 and c +1.5 before the next rise of 2
	check(t, "weighted by cost", by(u), `{"skipli":4.143,"dotfiles":3.857}`)

	u = fold(u, row("z", "/somewhere/else", 1, 48), row("z", "/somewhere/else", 2, 50), row("q", "/r/skipli", 0, 52))
	check(t, "unmapped and unexplained go to other", by(u)["other"], `4`)

	// A new window starts over.
	u = fold(u, row("a", "/r/skipli", 7, 3, 1900000000))
	check(t, "new window resets", []any{u.ResetsAt, u.WeekPct, by(u)}, `[1900000000,3,{"skipli":3}]`)
}

func TestFoldBaselineOnly(t *testing.T) {
	r := row("r1", "/r/dot", 1, 73)
	r.ResetsAt = nil
	u := fold(Usage{}, r)
	check(t, "baseline-only shows", []any{u.View(nil, 1).WeekPct, u.View(nil, 1).Corps}, `[73,[]]`)
	r2 := row("r1", "/r/dot", 2, 74)
	r2.ResetsAt = nil
	check(t, "rise after a baseline-only record", by(fold(u, r2)), `{"dotfiles":1}`)
}

func TestFoldFiveHour(t *testing.T) {
	r := row("f1", "/r/dot", 1, 5)
	r.FivePct, r.FiveResetsAt = ptr(31.0), ptr(1799990000.0)
	u := fold(Usage{}, r)
	check(t, "five-hour in view", u.View(nil, 1).FiveHour, `{"pct":31,"resets_at":1799990000}`)
	// Old rows without it still fold; their week_pct is ignored.
	u = fold(u, row("f1", "/r/dot", 2, 6))
	check(t, "row without five-hour folds, its week_pct ignored", []any{u.WeekPct, u.FivePct}, `[5,31]`)
}

func TestFoldManualReset(t *testing.T) {
	// A manual reset keeps resets_at but drops week_pct; stale rows without
	// five_pct (a session's first render) neither reset nor fake a rise.
	mk := func(ts float64, s string, cost, week float64, five ...float64) Row {
		r := row(s, "/r/dot", cost, week, 1792900800)
		r.Ts = ts
		if len(five) > 0 {
			r.FivePct, r.FiveResetsAt = ptr(five[0]), ptr(1792700000.0)
		}
		return r
	}
	u := Usage{}.Fold([]Row{mk(1, "s1", 1, 80, 10), mk(2, "s1", 2, 81, 11), mk(3, "s2", 0, 75), mk(4, "s1", 3, 81, 12), mk(5, "s1", 4, 92, 14), mk(6, "s3", 0, 92),
		mk(7, "s1", 5, 0, 1), mk(8, "s1", 6, 1, 2)}, nil, 0)
	rounded := map[string]float64{}
	for k, v := range u.ByCorps {
		rounded[k] = math.Round(v)
	}
	check(t, "manual reset starts a new window", []any{u.WeekPct, rounded}, `[1,{"other":1}]`)
}

func TestFoldIsPure(t *testing.T) {
	u := fold(Usage{}, row("a", "/r/skipli", 0, 40))
	before := js(t, u)
	_ = fold(u, row("a", "/r/skipli", 3, 41))
	check(t, "input record untouched", js(t, u), mustJSON(t, before))
}

func TestForgetsIdleSessions(t *testing.T) {
	a := row("old", "/r/dot", 0, 1)
	a.Ts = 1
	b := row("new", "/r/dot", 0, 1)
	b.Ts = 1209601 + 10
	u := fold(Usage{}, a, b)
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

func TestPathMap(t *testing.T) {
	st := &State{Berthier: ptr("b:p1"), Corps: []Corps{
		{Name: "skipli", RepoRoot: ptr("/r/skipli"), Divisions: []UnitState{{Name: "dark-mode", Path: ptr("/h/dark-mode")}}},
		{Name: ".dotfiles", RepoRoot: ptr("/r/dot")}},
		BerthierDivisions: []UnitState{{Name: "console", Path: ptr("/h/console")}}}
	st.Agents.Set("a:p1", &Agent{Cwd: ptr("/r/skipli"), WorkspaceLabel: ptr("skipli")})
	st.Agents.Set("d:p1", &Agent{Cwd: ptr("/h/dark-mode/src"), WorkspaceLabel: ptr("dark-mode")})
	st.Agents.Set("b:p1", &Agent{Cwd: ptr("/"), WorkspaceLabel: ptr("berthier")})
	m := PathMap(st, Divisions{"dark-mode": {Corps: "skipli"}})
	check(t, "path map", m, `[{"Path":"/r/skipli","Corps":"skipli"},{"Path":"/h/dark-mode/src","Corps":"skipli"},{"Path":"/r/skipli","Corps":"skipli"},{"Path":"/h/dark-mode","Corps":"skipli"},{"Path":"/r/dot","Corps":"dotfiles"},{"Path":"/h/console","Corps":"berthier"}]`)
	if c, ok := corpsOf(ptr("/h/dark-mode/src/x"), m); !ok || c != "skipli" {
		t.Error("longest path")
	}
}
