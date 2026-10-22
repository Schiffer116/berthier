package core

import (
	"math"
	"sort"
	"strings"
)

// Weekly quota attribution, pure. `berthier sample` appends one Row per change
// of a session's status line to the ledger; Fold turns new rows into the
// usage record (state/usage.json), so each Corps knows how much of the
// account's seven-day quota it used.
//
// Each rise in week_pct is split across the sessions whose cost rose since the
// last rise, weighted by that rise in cost, and credited to the Corps their cwd
// belongs to (a Division counts toward its Corps; no Corps: "other"). A rise
// no session's cost explains goes to "other". The first row ever seen is only a
// baseline: what the account used before sampling started belongs to no one.

// Row is one ledger line.
type Row struct {
	Ts           float64  `json:"ts"`
	Session      string   `json:"session"`
	Cwd          *string  `json:"cwd"`
	Cost         float64  `json:"cost"`
	WeekPct      float64  `json:"week_pct"`
	ResetsAt     *float64 `json:"resets_at"`
	FivePct      *float64 `json:"five_pct,omitempty"`
	FiveResetsAt *float64 `json:"five_resets_at,omitempty"`
}

// SessionUse is what the record remembers of a session.
type SessionUse struct {
	Cost  float64 `json:"cost"`
	Corps string  `json:"corps"`
	Ts    float64 `json:"ts"`
}

// Usage is state/usage.json.
type Usage struct {
	ResetsAt     *float64              `json:"resets_at"`
	WeekPct      *float64              `json:"week_pct"`
	FivePct      *float64              `json:"five_pct,omitempty"`
	FiveResetsAt *float64              `json:"five_resets_at,omitempty"`
	ByCorps      map[string]float64    `json:"by_corps"`
	Acc          map[string]float64    `json:"acc"`
	Sessions     map[string]SessionUse `json:"sessions"`
	Offset       int64                 `json:"offset"`
}

// PathCorps maps a working directory prefix to the Corps it counts toward.
type PathCorps struct{ Path, Corps string }

// PathMap lists where each Corps's Agents and Divisions work, from the Grand
// Army State. The longest path wins, so a Division's worktree beats its repo.
func PathMap(s *State, d Divisions) []PathCorps {
	b := "berthier"
	if a := s.Agents.Get(deref(s.Berthier)); a != nil && a.WorkspaceLabel != nil {
		b = CorpsName(*a.WorkspaceLabel)
	}
	var m []PathCorps
	for _, k := range s.Agents.Keys {
		a := s.Agents.M[k]
		if a.Cwd != nil && *a.Cwd != "/" {
			m = append(m, PathCorps{*a.Cwd, d.Owner(deref(a.WorkspaceLabel))})
		}
	}
	for _, c := range s.Corps {
		n := CorpsName(c.Name)
		if c.RepoRoot != nil {
			m = append(m, PathCorps{*c.RepoRoot, n})
		}
		for _, dv := range c.Divisions {
			if dv.Path != nil {
				m = append(m, PathCorps{*dv.Path, n})
			}
		}
	}
	for _, dv := range s.BerthierDivisions {
		if dv.Path != nil {
			m = append(m, PathCorps{*dv.Path, b})
		}
	}
	return m
}

func corpsOf(cwd *string, m []PathCorps) (string, bool) {
	if cwd == nil {
		return "", false
	}
	best, found := PathCorps{}, false
	for _, e := range m {
		if (*cwd == e.Path || strings.HasPrefix(*cwd, e.Path+"/")) && (!found || len(e.Path) >= len(best.Path)) {
			best, found = e, true
		}
	}
	return best.Corps, found
}

// newWindow: a row from another window: resets_at moved or the quota fell
// sharply (a manual reset keeps resets_at), or (no resets_at) the quota fell
// at all.
func (u *Usage) newWindow(r Row) bool {
	if u.WeekPct == nil {
		return false
	}
	if r.ResetsAt != nil && u.ResetsAt != nil {
		return math.Abs(*r.ResetsAt-*u.ResetsAt) > 3600 || *u.WeekPct-r.WeekPct >= 10 || r.WeekPct < *u.WeekPct/2
	}
	return r.WeekPct < *u.WeekPct
}

// stale: a row without five_pct once the record has seen it is a session's
// first render, carrying a cached week_pct. Its cost counts, its week_pct does not.
func (u *Usage) stale(r Row) bool { return u.FivePct != nil && r.FivePct == nil }

func (u *Usage) rise(amt float64) {
	keys := make([]string, 0, len(u.Acc))
	total := 0.0
	for k := range u.Acc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		total += u.Acc[k]
	}
	if total > 0 {
		for _, k := range keys {
			c := "other"
			if s, ok := u.Sessions[k]; ok {
				c = s.Corps
			}
			u.ByCorps[c] += amt * u.Acc[k] / total
		}
	} else {
		u.ByCorps["other"] += amt
	}
	u.Acc = map[string]float64{}
}

func (u *Usage) step(r Row, m []PathCorps) {
	stale := u.stale(r)
	if !stale && u.newWindow(r) {
		z := 0.0
		u.WeekPct = &z
		u.ByCorps = map[string]float64{}
		u.Acc = map[string]float64{}
	}
	s, seen := u.Sessions[r.Session]
	corps, ok := corpsOf(r.Cwd, m)
	if !ok {
		corps = "other"
		if seen {
			corps = s.Corps
		}
	}
	d := 0.0
	if seen {
		d = math.Max(r.Cost-s.Cost, 0)
	}
	u.Sessions[r.Session] = SessionUse{Cost: r.Cost, Corps: corps, Ts: r.Ts}
	if d > 0 {
		u.Acc[r.Session] += d
	}
	if !stale {
		switch {
		case u.WeekPct == nil:
			w := r.WeekPct
			u.WeekPct = &w
		case r.WeekPct > *u.WeekPct:
			u.rise(r.WeekPct - *u.WeekPct)
			w := r.WeekPct
			u.WeekPct = &w
		}
		if r.ResetsAt != nil {
			u.ResetsAt = r.ResetsAt
		}
	}
	if r.FivePct != nil {
		u.FivePct, u.FiveResetsAt = r.FivePct, r.FiveResetsAt
	}
}

// Fold returns u folded over rows in ledger order; sessions idle for two
// weeks are forgotten. offset is how many ledger bytes the record has seen.
func (u Usage) Fold(rows []Row, m []PathCorps, offset int64) Usage {
	u.Acc = copyMap(u.Acc)
	u.ByCorps = copyMap(u.ByCorps)
	sessions := make(map[string]SessionUse, len(u.Sessions))
	for k, v := range u.Sessions {
		sessions[k] = v
	}
	u.Sessions = sessions
	latest := 0.0
	for _, r := range rows {
		u.step(r, m)
		latest = math.Max(latest, r.Ts)
	}
	for k, s := range u.Sessions {
		if !(s.Ts > latest-1209600) {
			delete(u.Sessions, k)
		}
	}
	u.Offset = offset
	return u
}

func copyMap(m map[string]float64) map[string]float64 {
	c := make(map[string]float64, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// CorpsUse is one Corps's share of the week against its allowance.
type CorpsUse struct {
	Name      string   `json:"name"`
	Used      float64  `json:"used"`
	Allowance *float64 `json:"allowance"`
	Ratio     *float64 `json:"ratio"`
}

// FiveHour is the account's five-hour window.
type FiveHour struct {
	Pct      float64  `json:"pct"`
	ResetsAt *float64 `json:"resets_at"`
}

// UsageView is the usage record read for people and the Main Loop.
type UsageView struct {
	ResetsAt *float64   `json:"resets_at"`
	WeekPct  *float64   `json:"week_pct"`
	FiveHour *FiveHour  `json:"five_hour"`
	Corps    []CorpsUse `json:"corps"`
}

// View lists each Corps with usage or a budget this week, hungriest first
// (allowance and ratio nil without a budget). A record whose week has ended
// counts for nothing until the next sample.
func (u Usage) View(budgets map[string]float64, now int64) UsageView {
	n := float64(now)
	live := orF(u.ResetsAt, n+1) > n
	used := map[string]float64{}
	if live {
		used = u.ByCorps
	}
	b := map[string]float64{}
	for k, v := range budgets {
		b[CorpsName(k)] = v
	}
	names := map[string]bool{}
	for k := range used {
		names[k] = true
	}
	for k := range b {
		names[k] = true
	}
	v := UsageView{ResetsAt: u.ResetsAt, Corps: []CorpsUse{}}
	if live {
		v.WeekPct = u.WeekPct
	}
	if u.FivePct != nil && orF(u.FiveResetsAt, n+1) > n {
		v.FiveHour = &FiveHour{*u.FivePct, u.FiveResetsAt}
	}
	for name := range names {
		c := CorpsUse{Name: name, Used: used[name]}
		if a, ok := b[name]; ok {
			c.Allowance = &a
			if a > 0 {
				r := c.Used / a
				c.Ratio = &r
			}
		}
		v.Corps = append(v.Corps, c)
	}
	sort.SliceStable(v.Corps, func(i, j int) bool {
		a, b := v.Corps[i], v.Corps[j]
		if a.Used != b.Used {
			return a.Used > b.Used
		}
		return a.Name < b.Name
	})
	return v
}

func orF(p *float64, d float64) float64 {
	if p == nil {
		return d
	}
	return *p
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
