package core

import (
	"fmt"
	"testing"
)

func armyState() State {
	st := State{Berthier: ptr("b:p1"), UpdatedAt: 50,
		Corps: []Corps{
			{Name: "skipli", WorkspaceID: "wa", Agents: []string{"a:p1"}, Divisions: []UnitState{{Name: "dark-mode", WorkspaceID: "wd", Worktree: true, Agents: []string{"d:p1"}}}},
			{Name: ".dotfiles", WorkspaceID: "wc", Agents: []string{"c:p1"}, Divisions: []UnitState{}}}}
	add := func(pane, status, sid, label string) {
		st.Agents.Set(pane, &Agent{Status: status, SessionID: ptr(sid), WorkspaceLabel: ptr(label)})
	}
	add("a:p1", "working", "s-a", "skipli")
	add("b:p1", "idle", "s-b", "berthier")
	add("c:p1", "idle", "s-c", ".dotfiles")
	add("d:p1", "blocked", "s-d", "dark-mode")
	return st
}

func armyIn() ArmyIn {
	return ArmyIn{State: armyState(), Available: true, Now: 1000, Interval: 10, ReviveMax: 3,
		Reports: map[string]Report{
			"s-a":    {Line: "building offline sync"},
			"s-c":    {Line: "Needs you: push main?"},
			"s-b":    {Line: "watching the Grand Army"},
			"s-gone": {Line: "an old session"}}}
}

func TestArmyCountsAndNeeds(t *testing.T) {
	v := Army(armyIn())
	check(t, "counts", v.Counts, `{"working":1,"blocked":1,"done":0,"idle":2,"unknown":0,"gone":0}`)
	who := []string{}
	for _, a := range v.NeedsYou {
		who = append(who, a.Who)
	}
	check(t, "army needs you", who, `["dark-mode","dotfiles"]`)
	d := v.Corps[0].Divisions[0]
	check(t, "army division nested", []any{d.Name, d.Worktree, *d.Agents[0].Status}, `["dark-mode",true,"blocked"]`)
	check(t, "army berthier", []any{v.Berthier.Who, v.Berthier.Report}, `["berthier","watching the Grand Army"]`)
	units := [][]string{}
	for _, u := range v.Units {
		units = append(units, []string{u.Kind, u.Name})
	}
	check(t, "army units without an order", units, `[["corps","skipli"],["corps","dotfiles"],["berthier","berthier"]]`)
	check(t, "needs-you report stripped", *v.Corps[1].Agents[0].Report, `"push main?"`)
}

func ev(now, ago int64, kind, fields, msg string) string {
	f := ""
	if fields != "" {
		f = "," + fields
	}
	if msg == "" {
		msg = kind + " ..."
	}
	return fmt.Sprintf(`{"ts":%d,"kind":%q%s,"msg":%q}`, now-ago, kind, f, msg)
}

func TestArmyLog(t *testing.T) {
	in := armyIn()
	now := in.Now
	in.Log = []string{
		ev(now, 8*86400, "escalated", `"why":"long","to":"berthier","reason":"too old"`, ""),
		"2026-10-17T13:02:57+07:00 an old prose line",
		ev(now, 600, "loop_started", `"interval":10`, ""),
		ev(now, 300, "delivered", `"pane":"c:p1","from":"skipli","to":"dotfiles"`, ""),
		ev(now, 200, "spoke", `"pane":"a:p1","corps":"skipli","why":"blocked","to":"you"`, "spoke: skipli is waiting on you."),
		ev(now, 100, "notified", `"pane":"b:p1","corps":"berthier","why":"blocked","to":"you","reason":"blocked 2m on an approval or question"`, ""),
		ev(now, 100, "escalated", `"pane":"d:p1","corps":"dark-mode","why":"blocked","to":"skipli","reason":"blocked 3m on an approval or question"`, ""),
		ev(now, 100, "escalated", `"why":"pingpong","to":"berthier","ask_from":"dotfiles","ask_to":"skipli","reason":"10 back-and-forths in the last hour"`, ""),
		ev(now, 100, "escalated", `"why":"ask_stuck","to":"berthier","ask_from":"skipli","ask_to":"notes","reason":"undelivered 31m"`, ""),
	}
	in.Inbox = []Ask{ask("7-skipli", "skipli", "dotfiles", "unit", 5)}
	v := Army(in)
	tags := [][]any{}
	for _, e := range v.Activity[2:6] {
		tags = append(tags, []any{e["tag"], e["label"]})
	}
	check(t, "army activity newest first", tags, `[["escal","escal"],["escal","escal"],["speak","speak"],["ask","ask"]]`)
	check(t, "army log times", []any{v.Loop.StartedAt, v.Activity[0]["at"]}, fmt.Sprintf(`[%d,%d]`, now-600, now-100))
	esc := [][]any{}
	for _, e := range v.Escalations {
		esc = append(esc, []any{e.Label, e.To, e.Who, e.Why})
	}
	check(t, "army escalations 7d", esc,
		`[["ask stuck","berthier","skipli → notes","undelivered 31m"],["pingpong","berthier","dotfiles ⇄ skipli","10 back-and-forths in the last hour"],["blocked","skipli","dark-mode d:p1","blocked 3m on an approval or question"],["blocked","you","berthier b:p1","blocked 2m on an approval or question"],["speak","you","skipli","skipli is waiting on you."]]`)
	check(t, "army revive max", v.Loop.ReviveMax, `3`)
	check(t, "army asks", v.Asks, `[{"id":"7-skipli","from":"skipli","to":"dotfiles","text":"unit","at":5}]`)
}

func TestArmyHerdrOrder(t *testing.T) {
	in := armyIn()
	in.State.Order = []string{"wb", "wc", "wa", "we", "wd"}
	in.State.Corps[0].WorkspaceID, in.State.Corps[1].WorkspaceID = "wa", "wc"
	in.State.Corps[0].Divisions = []UnitState{{Name: "z-div", WorkspaceID: "we", Agents: []string{}}, {Name: "dark-mode", WorkspaceID: "wd", Worktree: true, Agents: []string{"d:p1"}}}
	in.State.Agents.M["b:p1"].WorkspaceID = "wb"
	v := Army(in)
	units := [][]any{}
	for _, u := range v.Units {
		ds := []string{}
		for _, d := range u.Divisions {
			ds = append(ds, d.Name)
		}
		units = append(units, []any{u.Name, ds})
	}
	check(t, "army units in herdr order", units, `[["berthier",[]],["dotfiles",[]],["skipli",["z-div","dark-mode"]]]`)
}

func TestArmyEmpty(t *testing.T) {
	v := Army(ArmyIn{})
	check(t, "empty counts", v.Counts, `{"working":0,"blocked":0,"done":0,"idle":0,"unknown":0,"gone":0}`)
	check(t, "empty lists are lists", []any{v.Corps, v.Units, v.NeedsYou, v.Asks, v.Activity, v.Escalations, v.AskLog}, `[[],[],[],[],[],[],[]]`)
}

func TestArmyCostAndDivisionModel(t *testing.T) {
	in := armyIn()
	in.Costs = map[string]float64{"s-d": 1.5}
	in.Divisions = Divisions{"dark-mode": {Corps: "skipli", Worktree: true, Model: "opus"}}
	v := Army(in)
	d, a := v.Corps[0].Divisions[0].Agents[0], v.Corps[0].Agents[0]
	check(t, "army session cost and division model", [][]any{{d.Cost, d.DivisionModel}, {a.Cost, a.DivisionModel}}, `[[1.5,"opus"],[null,null]]`)
}
