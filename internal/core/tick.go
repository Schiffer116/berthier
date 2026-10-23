package core

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The pure decision core of the Main Loop. Tick takes the previous Grand Army
// State, Herdr's snapshot, the clock and everything else it needs, and returns
// the next State plus the actions to carry out. It performs none of them and
// reads nothing: the caller (internal/cli) observes, acts, logs and writes.
//
// A decision that needs a pane read waits: the pane is listed under Look and
// the caller runs Tick again with Obs. Tick alone writes the Grand Army State.
// Events are Tick's decisions for loop.log; each action carries Ev, the
// fields of the event the caller logs for its outcome.

// Config is the Main Loop's tunables (BERTHIER_* variables, seconds unless noted).
type Config struct {
	BerthierRoot   string
	BlockedAfter   int64
	LongAfter      int64
	DivisionSettle int64
	ReviveMax      int
	ReviveBackoff  int64
	ReviveReset    int64
	EscalationTTL  int64
	SpeakAfter     int64
	AskStuck       int64
	PingpongMax    int
	LimitGrace     int64
	LimitRetry     int64
	BudgetWarn     float64 // percent of an allowance
	// Restart resume: a gap this long since the last state write may have cut
	// Agents off mid-turn; wait RestartWait for each to reappear (idle for
	// RestartSettle), then prompt it once.
	RestartGap, RestartWait, RestartSettle int64
}

// Snapshot is `herdr api snapshot`'s .result.snapshot.
type Snapshot struct {
	Workspaces []Workspace `json:"workspaces"`
	Agents     []SnapAgent `json:"agents"`
	Panes      []SnapAgent `json:"panes"`
}

type Workspace struct {
	ID       string `json:"workspace_id"`
	Label    string `json:"label"`
	Number   int    `json:"number"`
	Worktree *struct {
		RepoRoot     *string `json:"repo_root"`
		CheckoutPath *string `json:"checkout_path"`
	} `json:"worktree"`
}

// SnapAgent is an agent, or a pane that may still hold a session, in a snapshot.
type SnapAgent struct {
	PaneID       string  `json:"pane_id"`
	WorkspaceID  string  `json:"workspace_id"`
	TabID        *string `json:"tab_id"`
	Agent        *string `json:"agent"`
	Status       string  `json:"agent_status"`
	Cwd          *string `json:"cwd"`
	Title        *string `json:"terminal_title_stripped"`
	Name         *string `json:"name"`
	StateSeq     *int64  `json:"state_change_seq"`
	AgentSession *struct {
		Value *string `json:"value"`
	} `json:"agent_session"`
}

func (a SnapAgent) session() *string {
	if a.AgentSession == nil {
		return nil
	}
	return a.AgentSession.Value
}

// Ask is a pending `berthier ask` (a file of state/inbox).
type Ask struct {
	ID   string `json:"id"`
	Kind string `json:"kind,omitempty"` // "notify": one-way, from a service
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

// Obs is what the caller read from a pane Tick asked to look at.
type Obs struct {
	Exited      *bool
	AtShell     *bool
	LimitSeen   bool // limit_reset was read (LimitReset nil: no limit stop)
	LimitReset  *int64
	Interrupted *bool // its transcript ends mid-turn
}

// TickIn is everything Tick decides from.
type TickIn struct {
	Prev      State
	Snap      Snapshot
	Now       int64
	Cfg       Config
	Divisions Divisions
	Inbox     []Ask
	Usage     Usage
	Budgets   map[string]float64
	Obs       map[string]Obs
}

// Fields are an event's fields: whichever of pane, corps, name, session, to,
// why, ... it has.
type Fields map[string]any

// Event is a decision for loop.log.
type Event struct {
	Kind   string
	Msg    string
	Fields Fields
}

// Look asks the caller to read a pane.
type Look struct {
	PaneID    string   `json:"pane_id"`
	SessionID *string  `json:"session_id"`
	Want      []string `json:"want"`
}

type Revive struct {
	PaneID    string `json:"pane_id"`
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	Ev        Fields `json:"ev"`
}

type Rename struct {
	PaneID    string `json:"pane_id"`
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	Ev        Fields `json:"ev"`
}

type Resume struct {
	PaneID  string `json:"pane_id"`
	Name    string `json:"name"`
	ResetAt int64  `json:"reset_at"`
	Ev      Fields `json:"ev"`
}

// Escalation goes to a Main Agent (To is its pane; nil: Berthier), or, for
// Berthier's own trouble, to the user.
type Escalation struct {
	Key    string  `json:"key"`
	PaneID *string `json:"pane_id"`
	Name   *string `json:"name,omitempty"`
	To     *string `json:"to"`
	Head   string  `json:"head,omitempty"`
	Text   string  `json:"text"`
	Ev     Fields  `json:"ev"`
}

// restartTries is how many ticks a restart's continue nudge is attempted.
const restartTries = 5

// Proceed prompts an Agent a restart cut off mid-turn to continue.
type Proceed struct {
	PaneID string `json:"pane_id"`
	Name   string `json:"name"`
	Ev     Fields `json:"ev"`
}

type Speak struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	Ev   Fields `json:"ev"`
}

type Delivery struct {
	ID     string `json:"id"`
	PaneID string `json:"pane_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Text   string `json:"text"`
	Oneway bool   `json:"oneway"`
	Ev     Fields `json:"ev"`
}

// TickOut is the next state and the actions to carry out.
type TickOut struct {
	State    State        `json:"state"`
	Look     []Look       `json:"look"`
	Events   []Event      `json:"events"`
	Revive   []Revive     `json:"revive"`
	Rename   []Rename     `json:"rename"`
	Resume   []Resume     `json:"resume"`
	Proceed  []Proceed    `json:"proceed"`
	Escalate []Escalation `json:"escalate"`
	Notify   []Escalation `json:"notify"`
	Speak    []Speak      `json:"speak"`
	Deliver  []Delivery   `json:"deliver"`
}

func ptr[T any](v T) *T { return &v }

func strOr(p *string, d string) string {
	if p == nil {
		return d
	}
	return *p
}

func eqStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func inStrs(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isBerthierCwd(cwd *string, root string) bool {
	c := strOr(cwd, "")
	return c == root || strings.HasPrefix(c, root+"/")
}

func minutes(secs int64) int64 { return int64(math.Floor(float64(secs) / 60)) }

func round1(f float64) string { return Num(math.Round(f*10) / 10) }

// Tick decides one pass of the Main Loop.
func Tick(in TickIn) TickOut {
	cfg, now, div := in.Cfg, in.Now, in.Divisions
	obs := func(p string) Obs { return in.Obs[p] }
	corpsOfLabel := func(a *Agent) string { return div.Owner(strOr(a.WorkspaceLabel, "")) }

	// Herdr's own workspace order, which every view of the Grand Army follows.
	workspaces := append([]Workspace(nil), in.Snap.Workspaces...)
	sort.SliceStable(workspaces, func(i, j int) bool { return workspaces[i].Number < workspaces[j].Number })
	ws := map[string]Workspace{}
	for _, w := range workspaces {
		ws[w.ID] = w
	}
	paneIDs := map[string]bool{}
	for _, p := range in.Snap.Panes {
		paneIDs[p.PaneID] = true
	}

	// --- Weekly budgets: a Corps at its allowance has its Revives, usage-limit
	// resumes and ask deliveries held (soft: nothing is stopped), and its Main
	// Agent and the user hear at the warning level, once per week.
	var budget []CorpsUse
	for _, c := range in.Usage.View(in.Budgets, now).Corps {
		if c.Allowance != nil && *c.Allowance > 0 {
			budget = append(budget, c)
		}
	}
	week := Num(orF(in.Usage.ResetsAt, 0))
	over := map[string]bool{}
	for _, c := range budget {
		if *c.Ratio >= 1 {
			over[c.Name] = true
		}
	}
	prevAgents := &in.Prev.Agents
	prevEsc := in.Prev.Escalated

	// --- Live agents: carry over memory (session id, timers, revive bookkeeping).
	// A pane that still holds a Claude session with no agent listed is Herdr
	// resuming it after a restart (a crash clears the pane's session): it is live.
	agentPanes := map[string]bool{}
	for _, a := range in.Snap.Agents {
		agentPanes[a.PaneID] = true
	}
	listed := append([]SnapAgent(nil), in.Snap.Agents...)
	for _, p := range in.Snap.Panes {
		if p.session() != nil && !agentPanes[p.PaneID] {
			listed = append(listed, p)
		}
	}
	live := Agents{}
	for _, a := range listed {
		p := prevAgents.Get(a.PaneID)
		var pv Agent
		if p != nil {
			pv = *p
		}
		same := p != nil && pv.Status == a.Status
		sid := a.session()
		if sid == nil {
			sid = pv.SessionID
		}
		var name *string
		if a.Name != nil {
			name = a.Name
		} else if eqStr(sid, pv.SessionID) {
			name = pv.Name
		}
		kind := a.Agent
		if kind == nil {
			kind = pv.Kind
		}
		v := &Agent{
			Name:           name,
			Kind:           kind,
			Status:         a.Status,
			StatusSince:    now,
			PrevStatus:     nilIfEmpty(pv.Status),
			SessionID:      sid,
			WorkspaceID:    a.WorkspaceID,
			TabID:          a.TabID,
			Cwd:            a.Cwd,
			Title:          a.Title,
			StateSeq:       a.StateSeq,
			Limit:          pv.Limit,
			LimitResumedAt: pv.LimitResumedAt,
			LastReviveAt:   pv.LastReviveAt,
		}
		if w, ok := ws[a.WorkspaceID]; ok {
			v.WorkspaceLabel = ptr(w.Label)
		}
		if same {
			v.StatusSince, v.PrevStatus = pv.StatusSince, pv.PrevStatus
		}
		if orI(pv.LastReviveAt, 0) > now-cfg.ReviveReset {
			v.ReviveAttempts = pv.ReviveAttempts
		}
		live.Set(a.PaneID, v)
	}

	// --- Agents that vanished. A pane still present means the process died and
	// left its shell behind: keep it as gone and try to Revive. A closed pane is
	// a deliberate stop: forget the agent.
	gone := Agents{}
	for _, k := range prevAgents.Keys {
		a := prevAgents.M[k]
		if live.Get(k) != nil || a.SessionID == nil || strOr(a.Kind, "") != "claude" || !paneIDs[k] {
			continue
		}
		c := *a
		if c.Status != "gone" {
			c.PrevStatus, c.Status, c.StatusSince = nilIfEmpty(c.Status), "gone", now
		}
		if w, ok := ws[c.WorkspaceID]; ok {
			c.WorkspaceLabel = ptr(w.Label)
		}
		gone.Set(k, &c)
	}

	// --- Agents that vanished this very tick, and gone ones due a Revive.
	var vanished, due []string
	for _, k := range gone.Keys {
		g := gone.M[k]
		if prevAgents.M[k].Status != "gone" {
			vanished = append(vanished, k)
		}
		if g.ReviveAttempts < cfg.ReviveMax && !over[corpsOfLabel(g)] && now-orI(g.LastReviveAt, 0) >= cfg.ReviveBackoff {
			due = append(due, k)
		}
	}
	// A clean exit is the user's choice: forget the agent instead of Reviving.
	// Read when it vanishes, while the evidence is still at the bottom of its
	// pane, and again right before any Revive.
	exitCheck := uniq(append(append([]string(nil), vanished...), due...))
	var exited, reviving []string
	for _, k := range exitCheck {
		if o := obs(k); o.Exited != nil && *o.Exited {
			exited = append(exited, k)
		}
	}
	for _, k := range due {
		if o := obs(k); o.Exited != nil && !*o.Exited && o.AtShell != nil {
			reviving = append(reviving, k)
		}
	}
	remaining := Agents{}
	for _, k := range gone.Keys {
		if !inStrs(exited, k) {
			remaining.Set(k, gone.M[k])
		}
	}
	gone = remaining

	agents := Agents{}
	for _, k := range live.Keys {
		agents.Set(k, live.M[k])
	}
	for _, k := range gone.Keys {
		agents.Set(k, gone.M[k])
	}
	// Berthier: the first live agent in Berthier's own repository, else a gone one.
	var berthier *string
	for pass := 0; pass < 2 && berthier == nil; pass++ {
		for _, k := range agents.Keys {
			a := agents.M[k]
			if isBerthierCwd(a.Cwd, cfg.BerthierRoot) && (pass == 1 || a.Status != "gone") {
				berthier = ptr(k)
				break
			}
		}
	}
	berthierWS := ""
	if berthier != nil {
		berthierWS = agents.M[*berthier].WorkspaceID
	}
	// Who an event is about, by pane.
	about := func(p string) Fields {
		a := agents.Get(p)
		if a == nil {
			a = prevAgents.Get(p)
		}
		corps, name := "", FallbackName(p)
		if a != nil {
			corps = CorpsName(strOr(a.WorkspaceLabel, ""))
			if a.Name != nil {
				name = *a.Name
			}
		}
		if berthier != nil && p == *berthier {
			corps = "berthier"
		}
		return Fields{"pane": p, "corps": corps, "name": name}
	}
	with := func(f Fields, kv ...any) Fields {
		g := Fields{}
		for k, v := range f {
			g[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			g[kv[i].(string)] = kv[i+1]
		}
		return g
	}
	nameOf := func(k string, a *Agent) string {
		if a.Name != nil {
			return *a.Name
		}
		return FallbackName(k)
	}

	// --- Revive: resume the gone session in the pane it died in, if the pane is
	// back at its shell. Every try counts towards revive_max.
	revive := []Revive{}
	for _, k := range reviving {
		if o := obs(k); *o.AtShell {
			a := agents.M[k]
			revive = append(revive, Revive{k, nameOf(k, a), strOr(a.SessionID, ""), with(about(k), "session", a.SessionID)})
		}
	}

	// --- Names Herdr lost: give each listed agent back its session's name,
	// unless another agent took it meanwhile.
	taken := map[string]bool{}
	for _, a := range in.Snap.Agents {
		if a.Name != nil {
			taken[*a.Name] = true
		}
	}
	rename := []Rename{}
	for _, a := range in.Snap.Agents {
		v := live.M[a.PaneID]
		if a.Name != nil || v.Name == nil || !eqStr(a.session(), v.SessionID) || taken[*v.Name] {
			continue
		}
		rename = append(rename, Rename{a.PaneID, *v.Name, strOr(v.SessionID, ""), with(about(a.PaneID), "session", v.SessionID)})
	}

	// --- Usage limits: Claude often never continues by itself once its limit
	// resets. Read each idle episode's pane once for a limit stop; once the limit
	// has reset, nudge it once per episode, and never twice within limit_retry.
	// An episode is read on its first tick, so it stopped about now.
	type idleEp struct {
		pane    string
		a       *Agent
		episode string
	}
	var idle, limitCheck, limitRead []idleEp
	for _, k := range live.Keys {
		a := live.M[k]
		if a.Status == "idle" || a.Status == "done" {
			seq := "null"
			if a.StateSeq != nil {
				seq = fmt.Sprint(*a.StateSeq)
			}
			idle = append(idle, idleEp{k, a, fmt.Sprintf("%s:%d", seq, a.StatusSince)})
		}
	}
	for _, e := range idle {
		if e.a.Limit == nil || e.a.Limit.Episode != e.episode {
			limitCheck = append(limitCheck, e)
		}
	}
	for _, e := range limitCheck {
		if o := obs(e.pane); o.LimitSeen {
			c := *e.a
			c.Limit = &Limit{Episode: e.episode, Since: now, ResetAt: o.LimitReset}
			limitRead = append(limitRead, idleEp{e.pane, &c, e.episode})
		}
	}
	resume := []Resume{}
	for _, e := range idle {
		v := e.a
		for _, r := range limitRead {
			if r.pane == e.pane {
				v = r.a
				break
			}
		}
		if v.Limit != nil && v.Limit.Episode == e.episode && v.Limit.ResetAt != nil && !v.Limit.Resumed &&
			now >= *v.Limit.ResetAt+cfg.LimitGrace && now >= orI(v.LimitResumedAt, 0)+cfg.LimitRetry &&
			!over[corpsOfLabel(v)] {
			resume = append(resume, Resume{e.pane, nameOf(e.pane, v), *v.Limit.ResetAt, about(e.pane)})
		}
	}

	// --- Restart: Herdr resumes every session but they come back idle, even
	// those cut off mid-turn. A gap since the last state write (loop down,
	// Herdr unreachable) means the Agents working before it may have been
	// interrupted: remember them by session in the state until each reappears,
	// since Herdr may list them over several ticks. A reappeared idle one is
	// read for an unfinished turn in its transcript (Obs.Interrupted) and
	// prompted once if so; one that is working, blocked or finished is
	// forgotten, one never relisted expires.
	waiting := map[string]Interrupt{}
	for sid, w := range in.Prev.Interrupted {
		if w.Since > now-orDefault(cfg.RestartWait, 3600) {
			waiting[sid] = w
		}
	}
	if in.Prev.UpdatedAt != 0 && now-in.Prev.UpdatedAt > orDefault(cfg.RestartGap, 45) {
		for _, k := range prevAgents.Keys {
			a := prevAgents.M[k]
			if a.Status == "working" && strOr(a.Kind, "") == "claude" && a.SessionID != nil {
				waiting[*a.SessionID] = Interrupt{Name: a.Name, Since: now}
			}
		}
	}
	type present struct {
		pane, label, status string
		since               int64
	}
	bySession := map[string]present{}
	for _, k := range live.Keys {
		a := live.M[k]
		if a.SessionID == nil {
			continue
		}
		st := "unknown" // a pane Herdr is still resuming has no agent to prompt yet
		if agentPanes[k] {
			st = a.Status
		}
		bySession[*a.SessionID] = present{k, strOr(a.WorkspaceLabel, ""), st, a.StatusSince}
	}
	type back struct {
		sid  string
		name *string
		present
	}
	var backs, cutAgents, goAgents []back
	for _, sid := range sortedSids(waiting) {
		b, ok := bySession[sid]
		// Herdr first lists a resumed pane with its stale status, then
		// relaunches Claude: wait for the status to hold.
		if ok && (b.status == "idle" || b.status == "done") && now-b.since >= orDefault(cfg.RestartSettle, 20) {
			backs = append(backs, back{sid, waiting[sid].Name, b})
		}
	}
	for _, b := range backs {
		if o := obs(b.pane); o.Interrupted != nil && *o.Interrupted {
			cutAgents = append(cutAgents, b)
			if !over[div.Owner(b.label)] { // a Corps at its budget keeps its Agents waiting
				goAgents = append(goAgents, b)
			}
		}
	}
	inBacks := func(l []back, pane string) bool {
		for _, b := range l {
			if b.pane == pane {
				return true
			}
		}
		return false
	}
	// A nudge may not take (Herdr can still list the pane without its agent):
	// the session stays remembered with its tries so far. A taken one shows as
	// a working Agent or a finished turn on a later tick and is forgotten then;
	// after restartTries nudges a still-untaken one is given up on.
	proceed := []Proceed{}
	retry := map[string]int{}
	var gaveUp []Event
	for _, b := range goAgents {
		name := FallbackName(b.pane)
		if v := live.M[b.pane].Name; v != nil {
			name = *v
		} else if b.name != nil {
			name = *b.name
		}
		ev := with(about(b.pane), "session", b.sid)
		if tries := waiting[b.sid].Tries; tries >= restartTries {
			gaveUp = append(gaveUp, Event{"continue_failed", fmt.Sprintf("continue failed: %s in %s (restart): %d nudges did not take", name, b.pane, tries), ev})
			continue
		} else {
			retry[b.sid] = tries + 1
		}
		proceed = append(proceed, Proceed{b.pane, name, ev})
	}
	kept := map[string]Interrupt{}
	for sid, w := range waiting {
		b, ok := bySession[sid]
		if n, again := retry[sid]; again {
			kept[sid] = Interrupt{w.Name, w.Since, n}
		} else if !ok || b.status == "unknown" ||
			((b.status == "idle" || b.status == "done") &&
				(obs(b.pane).Interrupted == nil || (inBacks(cutAgents, b.pane) && !inBacks(goAgents, b.pane)))) {
			kept[sid] = w
		}
	}

	// --- Panes to read before the decisions above can be made.
	look := []Look{}
	for _, k := range exitCheck {
		if o := obs(k); o.Exited == nil {
			want := []string{"exited"}
			if inStrs(due, k) {
				want = append(want, "at_shell")
			}
			look = append(look, Look{k, prevAgents.M[k].SessionID, want})
		}
	}
	for _, e := range limitCheck {
		if !obs(e.pane).LimitSeen {
			look = append(look, Look{e.pane, e.a.SessionID, []string{"limit_reset"}})
		}
	}

	for _, b := range backs {
		if obs(b.pane).Interrupted == nil {
			look = append(look, Look{b.pane, ptr(b.sid), []string{"interrupted"}})
		}
	}

	// A Corps newly at its allowance this week: logged and spoken once.
	type block struct{ key, corps, text string }
	var blocks []block
	for _, c := range budget {
		key := fmt.Sprintf("budget_block:%s:%s", c.Name, week)
		if *c.Ratio >= 1 && prevEsc[key] == 0 {
			blocks = append(blocks, block{key, c.Name,
				fmt.Sprintf("%s used %s%% of the week, over its %s%% budget", c.Name, round1(c.Used), Num(*c.Allowance))})
		}
	}

	decided := append([]Event{}, gaveUp...)
	for _, k := range exited {
		f := with(about(k), "session", prevAgents.M[k].SessionID)
		decided = append(decided, Event{"forgot", fmt.Sprintf("forgot %s in %s: user exited it (session %s)", f["name"], k, strOr(prevAgents.M[k].SessionID, "null")), f})
	}
	for _, k := range reviving {
		if o := obs(k); !*o.AtShell {
			decided = append(decided, Event{"revive_skipped", fmt.Sprintf("revive skipped: pane %s is not at a shell prompt", k), about(k)})
		}
	}
	for _, e := range limitRead {
		if e.a.Limit.ResetAt == nil {
			continue
		}
		f := with(about(e.pane), "until", *e.a.Limit.ResetAt)
		decided = append(decided, Event{"limit_stop", fmt.Sprintf("usage limit: %s in %s stopped until %s", f["name"], e.pane,
			time.Unix(*e.a.Limit.ResetAt, 0).Local().Format("2006-01-02 15:04 MST")), f})
	}
	for _, b := range blocks {
		decided = append(decided, Event{"budget_exhausted",
			b.text + ": holding Revives, usage-limit resumes and ask delivery for it and its Divisions until the week resets",
			Fields{"corps": b.corps}})
	}

	// --- Each Corps's Main Agent: a live Agent in the Corps's own workspace.
	mainOf := map[string]string{} // by workspace label
	{
		byLabel := map[string][]string{}
		var labels []string
		for _, k := range agents.Keys {
			a := agents.M[k]
			if a.Status == "gone" || div.IsDivision(strOr(a.WorkspaceLabel, "")) {
				continue
			}
			l := strOr(a.WorkspaceLabel, "")
			if _, ok := byLabel[l]; !ok {
				labels = append(labels, l)
			}
			byLabel[l] = append(byLabel[l], k)
		}
		for _, l := range labels {
			mainOf[l] = byLabel[l][0]
		}
	}
	mainNamed := map[string]string{}
	{
		labels := make([]string, 0, len(mainOf))
		for l := range mainOf {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for _, l := range labels {
			mainNamed[CorpsName(l)] = mainOf[l]
		}
	}

	// --- Warning level reached: the Corps's Main Agent hears (Berthier when it has none).
	warnAt := cfg.BudgetWarn
	if warnAt == 0 {
		warnAt = 80
	}
	var warn []Escalation
	for _, c := range budget {
		if !(*c.Ratio >= warnAt/100 && *c.Ratio < 1) {
			continue
		}
		var to *string
		if p, ok := mainNamed[c.Name]; ok {
			to = ptr(p)
		}
		pct := int64(math.Floor(*c.Ratio * 100))
		var paneID *string
		if to != nil && berthier != nil && *to == *berthier {
			paneID = berthier
		}
		warn = append(warn, Escalation{
			Key: fmt.Sprintf("budget_warn:%s:%s", c.Name, week), PaneID: paneID, To: to,
			Head: "[berthier-loop] Weekly budget:",
			Text: fmt.Sprintf("%s has used %s%% of the week, %d%% of its %s%% budget: wind down low-value work, since at 100%% its Revives, usage-limit resumes and ask delivery are held",
				c.Name, round1(c.Used), pct, Num(*c.Allowance)),
			Ev: Fields{"why": "budget_warn", "reason": fmt.Sprintf("%d%% of weekly budget", pct), "corps": c.Name, "pct": pct},
		})
	}

	// --- Asks: deliver each to its addressee's Agent once it is ready for input,
	// at most one per Agent per tick, oldest first.
	type addressable struct{ pane, status, corps, name string }
	var addr []addressable
	for _, k := range agents.Keys {
		a := agents.M[k]
		if a.Status == "gone" {
			continue
		}
		if berthier != nil && k == *berthier {
			addr = append(addr, addressable{k, a.Status, "berthier", "berthier"})
		} else {
			addr = append(addr, addressable{k, a.Status, corpsOfLabel(a), CorpsName(strOr(a.WorkspaceLabel, ""))})
		}
	}
	inbox := append([]Ask(nil), in.Inbox...)
	sort.SliceStable(inbox, func(i, j int) bool { return inbox[i].At < inbox[j].At })
	deliver := []Delivery{}
	used := map[string]bool{}
	delivered := map[string]bool{}
	for _, m := range inbox {
		to := CorpsName(m.To)
		for _, o := range addr {
			if o.name == to && (o.status == "idle" || o.status == "done") && !over[o.corps] && !used[o.pane] {
				used[o.pane] = true
				delivered[m.ID] = true
				text := fmt.Sprintf("[berthier ask] from %s: %s (Reply or report back with: berthier ask %s \"...\")", m.From, m.Text, m.From)
				if m.Kind == "notify" {
					text = fmt.Sprintf("[berthier notify] from %s: %s", m.From, m.Text)
				}
				deliver = append(deliver, Delivery{m.ID, o.pane, m.From, to, text, m.Kind == "notify",
					Fields{"pane": o.pane, "from": m.From, "to": to, "ask": m.ID}})
				break
			}
		}
	}
	askLog := []AskLogEntry{}
	for _, e := range in.Prev.AskLog {
		if e.At > now-3600 {
			askLog = append(askLog, e)
		}
	}
	for _, d := range deliver {
		if !d.Oneway {
			askLog = append(askLog, AskLogEntry{d.From, d.To, now})
		}
	}

	// --- Escalation candidates. Keys embed status_since so a new episode of the
	// same condition escalates again, but one episode escalates only once.
	var cands []Escalation
	for _, pane := range agents.Keys {
		v := agents.M[pane]
		age := now - v.StatusSince
		where := strOr(v.WorkspaceLabel, v.WorkspaceID)
		who := fmt.Sprintf("agent '%s' in %s (pane %s)", strOr(v.Name, "-"), where, pane)
		label := strOr(v.WorkspaceLabel, "")
		// A Division's trouble goes to its Corps's Main Agent, which commands it.
		var to *string
		if div.IsDivision(label) {
			if p, ok := mainOf[div[CorpsName(label)].Corps]; ok {
				to = ptr(p)
			}
		}
		var why, reason, also string
		switch {
		case v.Status == "gone" && v.ReviveAttempts >= cfg.ReviveMax:
			why, reason, also = "revive_failed", fmt.Sprintf("revive failed %dx", v.ReviveAttempts), ", session "+strOr(v.SessionID, "null")
		case v.Status == "blocked" && age >= cfg.BlockedAfter:
			why, reason = "blocked", fmt.Sprintf("blocked %dm on an approval or question", minutes(age))
		case v.Status == "working" && age >= cfg.LongAfter:
			why, reason = "long", fmt.Sprintf("working %dm without stopping", minutes(age))
		// A spawned Division that stops may be done: its Corps reviews it.
		case div[CorpsName(label)].Worktree && (v.Status == "idle" || v.Status == "done") &&
			strOr(v.PrevStatus, "") == "working" && age >= cfg.DivisionSettle:
			why, reason = "division_stopped", "division agent stopped working (finished or stalled)"
		default:
			continue
		}
		cands = append(cands, Escalation{
			Key: fmt.Sprintf("%s:%s:%d", why, pane, v.StatusSince), PaneID: ptr(pane), Name: v.Name, To: to,
			Text: fmt.Sprintf("%s: %s%s", reason, who, also), Ev: with(about(pane), "why", why, "reason", reason),
		})
	}
	for _, m := range in.Inbox {
		if m.Kind == "notify" || now-m.At < cfg.AskStuck || delivered[m.ID] {
			continue
		}
		reason := fmt.Sprintf("undelivered %dm", minutes(now-m.At))
		cands = append(cands, Escalation{
			Key:  "ask_stuck:" + m.ID,
			Text: fmt.Sprintf("ask from %s to %s %s: %s", m.From, m.To, reason, m.Text),
			Ev:   Fields{"why": "ask_stuck", "reason": reason, "ask": m.ID, "ask_from": m.From, "ask_to": CorpsName(m.To)},
		})
	}
	// Ping-pong: replies flipping direction again and again; one-way updates
	// don't count, nor a Corps commanding and reviewing its own Division.
	{
		type entry struct {
			AskLogEntry
			pair [2]string
		}
		groups := map[[2]string][]entry{}
		for _, e := range askLog {
			if CorpsName(div[e.From].Corps) == e.To || CorpsName(div[e.To].Corps) == e.From {
				continue
			}
			pair := [2]string{e.From, e.To}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			groups[pair] = append(groups[pair], entry{e, pair})
		}
		pairs := make([][2]string, 0, len(groups))
		for p := range groups {
			pairs = append(pairs, p)
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i][0] != pairs[j][0] {
				return pairs[i][0] < pairs[j][0]
			}
			return pairs[i][1] < pairs[j][1]
		})
		for _, p := range pairs {
			l := groups[p]
			sort.SliceStable(l, func(i, j int) bool { return l[i].At < l[j].At })
			flips := 0
			for i := 1; i < len(l); i++ {
				if l[i].From != l[i-1].From {
					flips++
				}
			}
			if flips < cfg.PingpongMax {
				continue
			}
			reason := fmt.Sprintf("%d back-and-forths in the last hour", flips)
			pair := p[0] + " and " + p[1]
			cands = append(cands, Escalation{
				Key:  fmt.Sprintf("pingpong:%s:%d", pair, int64(math.Floor(float64(now)/3600))),
				Text: fmt.Sprintf("asks bouncing between %s: %s", pair, reason),
				Ev:   Fields{"why": "pingpong", "reason": reason, "ask_from": p[0], "ask_to": p[1]},
			})
		}
	}
	cands = append(cands, warn...)
	events := []Escalation{}
	for _, c := range cands {
		if prevEsc[c.Key] != 0 {
			continue
		}
		dest := "berthier"
		if c.To != nil && *c.To != "" {
			dest = CorpsName(strOr(agents.M[*c.To].labelOrNil(), ""))
		}
		c.Ev = with(c.Ev, "to", dest)
		events = append(events, c)
	}

	// --- Spoken alerts: the user is needed. Faster than escalation, because
	// they skip Berthier's judgment; deduplicated per episode like escalations.
	var speakAll []Speak
	for _, pane := range agents.Keys {
		v := agents.M[pane]
		age := now - v.StatusSince
		label := strOr(v.WorkspaceLabel, v.WorkspaceID)
		where := Spoken(label)
		if r, ok := div[CorpsName(strOr(v.WorkspaceLabel, ""))]; ok && r.Corps != "" {
			where += " in " + Spoken(r.Corps)
		}
		var why, text string
		switch {
		case v.Status == "blocked" && age >= cfg.SpeakAfter:
			why, text = "blocked", where+" is waiting on you"
		case v.Status == "gone" && v.ReviveAttempts >= cfg.ReviveMax:
			why, text = "revive_failed", where+" crashed and could not be resumed"
		default:
			continue
		}
		speakAll = append(speakAll, Speak{fmt.Sprintf("speak_%s:%s:%d", why, pane, v.StatusSince), text, with(about(pane), "why", why, "to", "you")})
	}
	for _, w := range warn {
		corps := w.Ev["corps"].(string)
		speakAll = append(speakAll, Speak{"speak_" + w.Key,
			fmt.Sprintf("%s has used %d percent of its weekly budget", Spoken(corps), w.Ev["pct"]),
			Fields{"corps": corps, "why": "budget_warn", "to": "you"}})
	}
	for _, b := range blocks {
		speakAll = append(speakAll, Speak{"speak_" + b.key, Spoken(b.corps) + " is over its weekly budget",
			Fields{"corps": b.corps, "why": "budget_exhausted", "to": "you"}})
	}
	speak := []Speak{}
	for _, s := range speakAll {
		if prevEsc[s.Key] == 0 {
			speak = append(speak, s)
		}
	}

	// Berthier cannot escalate to itself: its own events go to the user instead.
	escalate, notify := []Escalation{}, []Escalation{}
	for _, e := range events {
		if e.PaneID != nil && berthier != nil && *e.PaneID == *berthier {
			e.Ev = with(e.Ev, "to", "you")
			notify = append(notify, e)
		} else {
			escalate = append(escalate, e)
		}
	}

	escalated := map[string]int64{}
	for k, v := range prevEsc {
		if v > now-cfg.EscalationTTL {
			escalated[k] = v
		}
	}
	for _, e := range events {
		escalated[e.Key] = now
	}
	for _, s := range speak {
		escalated[s.Key] = now
	}
	for _, b := range blocks {
		escalated[b.key] = now
	}

	// --- Corps: every workspace that is not a known Division and not Berthier's own.
	byWS := map[string][]string{}
	for _, k := range agents.Keys {
		a := agents.M[k]
		byWS[a.WorkspaceID] = append(byWS[a.WorkspaceID], k)
	}
	list := func(id string) []string {
		if l := byWS[id]; l != nil {
			return l
		}
		return []string{}
	}
	var divisionWS []Workspace
	for _, w := range workspaces {
		if div.IsDivision(w.Label) {
			divisionWS = append(divisionWS, w)
		}
	}
	unit := func(w Workspace) UnitState {
		u := UnitState{Name: w.Label, WorkspaceID: w.ID, Worktree: div[CorpsName(w.Label)].Worktree, Agents: list(w.ID)}
		if w.Worktree != nil {
			u.Path = w.Worktree.CheckoutPath
		}
		return u
	}
	corps := []Corps{}
	for _, w := range workspaces {
		if div.IsDivision(w.Label) || w.ID == berthierWS || w.Label == "berthier" {
			continue
		}
		c := Corps{Name: w.Label, WorkspaceID: w.ID, Agents: list(w.ID), Divisions: []UnitState{}}
		if w.Worktree != nil {
			c.RepoRoot = w.Worktree.RepoRoot
		}
		for _, d := range divisionWS {
			if div[CorpsName(d.Label)].Corps == w.Label {
				c.Divisions = append(c.Divisions, unit(d))
			}
		}
		corps = append(corps, c)
	}
	// Berthier's own Divisions (e.g. one coding Berthier itself) hang off Berthier.
	berthierDivisions := []UnitState{}
	var berthierLabel *string
	for _, w := range workspaces {
		if w.ID == berthierWS {
			berthierLabel = ptr(w.Label)
			break
		}
	}
	for _, d := range divisionWS {
		if berthierLabel != nil && div[CorpsName(d.Label)].Corps == *berthierLabel {
			berthierDivisions = append(berthierDivisions, unit(d))
		}
	}
	order := []string{}
	for _, w := range workspaces {
		order = append(order, w.ID)
	}

	// --- The Grand Army State: what was decided above, recorded.
	for _, e := range limitRead {
		agents.M[e.pane].Limit = e.a.Limit
	}
	for _, k := range reviving {
		agents.M[k].ReviveAttempts++
		agents.M[k].LastReviveAt = ptr(now)
	}
	for _, r := range resume {
		a := agents.M[r.PaneID]
		l := *a.Limit
		l.Resumed = true
		a.Limit = &l
		a.LimitResumedAt = ptr(now)
	}

	return TickOut{
		State: State{Version: 1, UpdatedAt: now, Berthier: berthier, Corps: corps, BerthierDivisions: berthierDivisions,
			Order: order, Agents: agents, Escalated: escalated, Interrupted: kept, AskLog: askLog},
		Look: look, Events: decided, Revive: revive, Rename: rename, Resume: resume, Proceed: proceed,
		Escalate: escalate, Notify: notify, Speak: speak, Deliver: deliver,
	}
}

func (a *Agent) labelOrNil() *string {
	if a == nil {
		return nil
	}
	return a.WorkspaceLabel
}

func orI(p *int64, d int64) int64 {
	if p == nil {
		return d
	}
	return *p
}

func uniq(l []string) []string {
	sort.Strings(l)
	out := l[:0]
	for i, s := range l {
		if i == 0 || s != l[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func orDefault(v, d int64) int64 {
	if v == 0 {
		return d
	}
	return v
}

func sortedSids(m map[string]Interrupt) []string {
	l := make([]string, 0, len(m))
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}
