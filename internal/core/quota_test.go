package core

import (
	"reflect"
	"testing"
)

func TestQuotaGuard(t *testing.T) {
	five := func(p, r float64) UsageView { return UsageView{FiveHour: &FiveHour{p, &r}} }
	week := func(p, r float64) UsageView { return UsageView{WeekPct: &p, ResetsAt: &r} }
	idle := QuotaAgent{Pane: "w1:p1", To: "a", Status: "idle"}
	work := QuotaAgent{Pane: "w2:p1", To: "b", Status: "working"}
	froz := QuotaAgent{Pane: "w3:p1", To: "c", Status: "idle", Frozen: true}
	draft := QuotaAgent{Pane: "w4:p1", To: "d", Status: "idle"}
	held := func(p string) bool { return p == "w4:p1" }
	all := []QuotaAgent{idle, work, froz, draft}
	panes := func(q quotaOut) []string {
		var p []string
		for _, d := range q.Deliver {
			p = append(p, d.PaneID)
		}
		return p
	}
	esc := func(q quotaOut, prev map[string]int64) map[string]int64 {
		m := map[string]int64{}
		for k, v := range prev {
			m[k] = v
		}
		for _, k := range q.Mark {
			m[k] = 1
		}
		return m
	}
	tests := []struct {
		name   string
		v      UsageView
		agents []QuotaAgent
		prev   map[string]int64
		want   []string
		events int
		speak  int
	}{
		{"89 stays quiet", five(89, 100), all, nil, nil, 0, 0},
		{"90 fires for idle only", five(90, 100), all, nil, []string{"w1:p1"}, 1, 1},
		{"custom pct above", five(95, 100), all, nil, []string{"w1:p1"}, 1, 1},
		{"already sent", five(95, 100), []QuotaAgent{idle}, map[string]int64{"quota_guard:five:100": 1, "quota_guard:five:100:w1:p1": 1}, nil, 0, 0},
		{"working one gets it on idle, same window", five(95, 100), []QuotaAgent{{Pane: "w2:p1", To: "b", Status: "idle"}}, map[string]int64{"quota_guard:five:100": 1}, []string{"w2:p1"}, 0, 0},
		{"window reset re-arms", five(90, 200), []QuotaAgent{idle}, map[string]int64{"quota_guard:five:100": 1, "quota_guard:five:100:w1:p1": 1}, []string{"w1:p1"}, 1, 1},
		{"week window", week(91, 500), []QuotaAgent{idle}, nil, []string{"w1:p1"}, 1, 1},
		{"both windows: one prompt", UsageView{FiveHour: &FiveHour{92, ptr(100.0)}, WeekPct: ptr(93.0), ResetsAt: ptr(500.0)}, []QuotaAgent{idle}, nil, []string{"w1:p1"}, 2, 2},
	}
	for _, tc := range tests {
		q := quotaGuard(tc.v, 90, tc.agents, held, tc.prev)
		if got := panes(q); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: delivered %v, want %v", tc.name, got, tc.want)
		}
		if len(q.Events) != tc.events || len(q.Speak) != tc.speak {
			t.Errorf("%s: %d events %d speaks, want %d %d", tc.name, len(q.Events), len(q.Speak), tc.events, tc.speak)
		}
	}
	// 89 -> 90 -> 91 fires once: marks fed back as the next tick's state.
	prev := map[string]int64{}
	var n int
	for _, p := range []float64{89, 90, 91, 95} {
		q := quotaGuard(five(p, 100), 0, []QuotaAgent{idle}, held, prev)
		n += len(q.Deliver)
		prev = esc(q, prev)
	}
	if n != 1 {
		t.Errorf("89->90->91->95 delivered %d times, want 1", n)
	}
	if q := quotaGuard(five(90, 100), 0, []QuotaAgent{idle}, held, nil); q.Deliver[0].Text[:len(TagQuota)] != TagQuota || OriginOf(q.Deliver[0].Text) != "quota" {
		t.Errorf("tag/origin: %q", q.Deliver[0].Text)
	}
}
