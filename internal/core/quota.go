package core

import "fmt"

// QuotaAgent is an Agent as the quota guard sees it.
type QuotaAgent struct {
	Pane, To, Status string
	Frozen           bool
}

type quotaOut struct {
	Deliver []Delivery
	Speak   []Speak
	Events  []Event
	Mark    []string // Escalated keys to record
}

// quotaAgents lists the live Agents for quotaGuard, skipping panes that
// already take an ask this tick.
func quotaAgents(agents Agents, frozen map[string]*Frozen, used map[string]bool) []QuotaAgent {
	var out []QuotaAgent
	for _, k := range agents.Keys {
		a := agents.M[k]
		if a.Status == "gone" || used[k] {
			continue
		}
		out = append(out, QuotaAgent{Pane: k, To: CorpsName(strOr(a.WorkspaceLabel, "")), Status: a.Status,
			Frozen: frozen[k] != nil})
	}
	return out
}

// quotaGuard: once an account window (five-hour, seven-day) is at pct% (0: 90)
// or more, each idle non-Frozen Agent gets the "quota" broadcast once per
// window (keys carry the window's resets_at); a working Agent gets it on its
// next idle within the same window. The window's first reach is logged and
// spoken once.
func quotaGuard(v UsageView, pct float64, agents []QuotaAgent, held func(string) bool, prevEsc map[string]int64) quotaOut {
	if pct <= 0 {
		pct = 90
	}
	type window struct {
		name, label string
		pct         float64
		resets      *float64
	}
	var ws []window
	if v.FiveHour != nil {
		ws = append(ws, window{"five", "five-hour", v.FiveHour.Pct, v.FiveHour.ResetsAt})
	}
	if v.WeekPct != nil {
		ws = append(ws, window{"week", "seven-day", *v.WeekPct, v.ResetsAt})
	}
	text, _ := BroadcastText("quota", "", 0)
	var q quotaOut
	sent := map[string]bool{} // a pane hears it once per tick, whichever windows tripped
	for _, w := range ws {
		if w.pct < pct {
			continue
		}
		key := fmt.Sprintf("quota_guard:%s:%s", w.name, Num(orF(w.resets, 0)))
		ev := Fields{"why": "quota_guard", "window": w.name, "pct": int(w.pct)}
		if prevEsc[key] == 0 {
			q.Mark = append(q.Mark, key)
			q.Events = append(q.Events, Event{"quota_guard", fmt.Sprintf("%s window at %s%% of quota: asking idle Agents for a short handoff", w.label, round1(w.pct)), ev})
			q.Speak = append(q.Speak, Speak{"speak_" + key, fmt.Sprintf("The %s quota is at %d percent", w.label, int(w.pct)), Fields{"why": "quota_guard", "window": w.name, "pct": int(w.pct), "to": "you"}})
		}
		for _, a := range agents {
			k := key + ":" + a.Pane
			if a.Frozen || (a.Status != "idle" && a.Status != "done") || prevEsc[k] != 0 || held(a.Pane) { // held last: it asks for a read of the input box
				continue
			}
			q.Mark = append(q.Mark, k)
			if sent[a.Pane] {
				continue
			}
			sent[a.Pane] = true
			q.Deliver = append(q.Deliver, Delivery{ID: k, PaneID: a.Pane, From: "service", To: a.To, Text: text, Oneway: true,
				Ev: Fields{"pane": a.Pane, "from": "service", "to": a.To, "ask": k, "why": "quota_guard", "window": w.name}})
		}
	}
	return q
}
