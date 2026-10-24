package core

import "testing"

func TestBroadcastText(t *testing.T) {
	for _, tc := range []struct {
		name, kind, extra string
		max               int
		want              string
		ok                bool
	}{
		{"shutdown", "shutdown", "", 100, TagShutdown + " " + broadcasts["shutdown"].text, true},
		{"shutdown extra", "shutdown", "  back at 9 ", 100, TagShutdown + " " + broadcasts["shutdown"].text + " back at 9", true},
		{"capped", "shutdown", "abcdefgh", 5, TagShutdown + " " + broadcasts["shutdown"].text + " abcde [truncated]", true},
		{"wake", "wake", "", 100, TagWake + " " + broadcasts["wake"].text, true},
		{"unknown", "reboot", "", 100, "", false},
	} {
		got, ok := BroadcastText(tc.kind, tc.extra, tc.max)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: %q %v, want %q %v", tc.name, got, ok, tc.want, tc.ok)
		}
		if ok && OriginOf(got) != tc.kind {
			t.Errorf("%s: origin %q", tc.name, OriginOf(got))
		}
	}
}

func TestBroadcastTargets(t *testing.T) {
	lbl := func(s string) *string { return &s }
	frozen := Frozen{}
	ag := Agents{Keys: []string{"b", "m", "d", "f", "g", "n"}, M: map[string]*Agent{
		"b": {Status: "idle", WorkspaceLabel: lbl("berthier")},
		"m": {Status: "working", WorkspaceLabel: lbl("Skipli")},
		"d": {Status: "blocked", WorkspaceLabel: lbl("shutdown-wake")},
		"f": {Status: "gone", WorkspaceLabel: lbl("old"), Frozen: &frozen},
		"g": {Status: "gone", WorkspaceLabel: lbl("dead")},
		"n": {Status: "idle"},
	}}
	to, skipped := BroadcastTargets(ag, lbl("b"))
	check(t, "targets", []any{to, skipped}, `[["berthier","skipli","shutdown-wake"],1]`)
}

func TestRebuild(t *testing.T) {
	check(t, "rebuild", []string{Rebuild("", ""), Rebuild("halfway", ""), Rebuild("halfway", "Build it")},
		`["","\nYour last report: halfway","\nYour last report: halfway\nYour branch holds your progress so far. Your original Brief:\n\nBuild it"]`)
	check(t, "fresh prompt carries it", FreshPrompt("halfway", "Build it") == "[berthier fresh] You were restarted fresh: run berthier notes, read your Board, your last report and git status, then continue."+Rebuild("halfway", "Build it"), `true`)
}
