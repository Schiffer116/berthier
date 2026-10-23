package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"berthier/internal/console"
	"berthier/internal/core"
)

// ArmyView is the Grand Army view model (`berthier army`).
func (e *Env) ArmyView() core.ArmyView {
	now := e.now()
	available := false
	if st, err := os.Stat(e.State); err == nil {
		available = float64(now-st.ModTime().Unix()) <= e.IntervalSecs*3
	}
	in := core.ArmyIn{Reports: e.ReadReports(), Inbox: e.ReadInbox(), Log: TailLines(e.Log, 2000),
		Budgets: e.ReadBudgets(), BudgetWarn: e.BudgetWarn, Available: available, Now: now,
		Interval: e.IntervalSecs, ReviveMax: e.ReviveMax}
	readJSON(e.State, &in.State)
	in.Divisions = e.ReadDivisions()
	readJSON(e.Usage, &in.Usage)
	in.Costs = map[string]float64{}
	for id, s := range in.Usage.Sessions {
		in.Costs[id] = s.Cost
	}
	in.Usage.Sessions, in.Usage.Acc = nil, nil
	return core.Army(in)
}

func cmdArmy(e *Env, args []string, io IO) error {
	if len(args) != 0 {
		return die("usage: berthier army")
	}
	return encode(io.Out, e.ArmyView())
}

// cmdCounts is the bar's compact view of `berthier army`: availability, Agent
// count per status, and "report", one line per live Agent ("<who>: <state> -
// <report>", needs-you first; <state> is "needs you" or the live status).
func cmdCounts(e *Env, args []string, io IO) error {
	if len(args) != 0 {
		return die("usage: berthier counts")
	}
	v := e.ArmyView()
	var agents []core.AgentView
	for _, c := range v.Corps {
		agents = append(agents, c.Agents...)
		for _, d := range c.Divisions {
			agents = append(agents, d.Agents...)
		}
	}
	if v.Berthier != nil {
		agents = append(agents, *v.Berthier)
	}
	for _, d := range v.BerthierDivisions {
		agents = append(agents, d.Agents...)
	}
	var live []core.AgentView
	for _, a := range agents {
		if a.Status == nil || *a.Status != "gone" {
			live = append(live, a)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].NeedsYou != live[j].NeedsYou {
			return live[i].NeedsYou
		}
		return live[i].Who < live[j].Who
	})
	lines := []string{}
	for _, a := range live {
		state, report := "null", "no report yet"
		if a.Status != nil {
			state = *a.Status
		}
		if a.NeedsYou {
			state = "needs you"
		}
		if a.Report != nil {
			report = *a.Report
		}
		lines = append(lines, fmt.Sprintf("%s: %s - %s", a.Who, state, report))
	}
	c := v.Counts
	return encode(io.Out, struct {
		Available bool `json:"available"`
		core.Counts
		Report string `json:"report"`
	}{v.Available, c, strings.Join(lines, "\n")})
}

// encode writes v as one line of JSON, without HTML escaping.
func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func cmdOwners(e *Env, args []string, io IO) error {
	b, err := os.ReadFile(filepath.Join(e.Root, "owners.md"))
	if err != nil {
		return err
	}
	io.Out.Write(b)
	return nil
}

// cmdLog prints the Main Loop's event log (state/loop.log, JSON Lines)
// readably: local time, kind, message. -n lines (default 40), -f to follow.
func cmdLog(e *Env, args []string, io IO) error {
	n, follow := 40, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			if i+1 >= len(args) {
				return die("usage: berthier log [-n lines] [-f]")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return die("usage: berthier log [-n lines] [-f]")
			}
			n = v
			i++
		case "-f":
			follow = true
		default:
			return die("usage: berthier log [-n lines] [-f]")
		}
	}
	if _, err := os.Stat(e.Log); err != nil {
		return nil
	}
	print := func(line string) {
		var m struct {
			Ts   float64
			Kind string
			Msg  string
		}
		if json.Unmarshal([]byte(line), &m) != nil || strings.TrimSpace(line) == "" || line[0] != '{' {
			return
		}
		pad := strings.Repeat(" ", max(0, 15-utf8.RuneCountInString(m.Kind)))
		fmt.Fprintf(io.Out, "%s  %s%s  %s\n", time.Unix(int64(m.Ts), 0).Format("2006-01-02 15:04:05"), m.Kind, pad, m.Msg)
	}
	for _, l := range TailLines(e.Log, n) {
		print(l)
	}
	if !follow {
		return nil
	}
	f, err := os.Open(e.Log)
	if err != nil {
		return nil
	}
	defer f.Close()
	f.Seek(0, 2)
	r := bufio.NewReader(f)
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		print(strings.TrimRight(l, "\n"))
	}
}

// --- budget

func (e *Env) corpsNames(withBerthier bool) []string {
	st := e.ReadState()
	set := map[string]bool{}
	if st.Berthier != nil && withBerthier {
		set["berthier"] = true
	}
	for _, c := range st.Corps {
		set[core.CorpsName(c.Name)] = true
	}
	return sortedKeys(set)
}

func sortedKeys[V any](m map[string]V) []string {
	l := make([]string, 0, len(m))
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}

func knownList(names []string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + " ")
	}
	return b.String()
}

// BadBudget is a SetBudget refusal caused by the request, not the disk.
type BadBudget string

func (b BadBudget) Error() string { return string(b) }

// Bad marks the refusal as the request's fault (the Console answers 400).
func (BadBudget) Bad() bool { return true }

// SetBudget sets Corps corps's weekly budget to pct % of the week, or drops it
// when pct is nil. The command and the Console both go through it.
func (e *Env) SetBudget(corps string, pct *float64) error {
	budgets := e.ReadBudgets()
	if pct == nil {
		if _, ok := budgets[corps]; !ok {
			return BadBudget(fmt.Sprintf("no budget set for '%s'", corps))
		}
		delete(budgets, corps)
	} else {
		if !(*pct > 0 && *pct <= 100) {
			return BadBudget("pct must be a number in (0, 100], or off: usage: berthier budget [<corps> <pct>|off]")
		}
		if st, err := os.Stat(e.State); err == nil && st.Size() > 0 {
			names := e.corpsNames(true)
			if _, set := budgets[corps]; !strsHas(names, corps) && !set {
				return BadBudget(fmt.Sprintf("no Corps '%s'; known: %s", corps, knownList(names)))
			}
		}
		budgets[corps] = *pct
	}
	return writeJSONAtomic(e.Budgets, budgets, true)
}

// cmdBudget: weekly quota budgets, soft. `budget` prints each Corps's share
// of the account's seven-day quota against its allowance (state/budgets.json,
// % of the week); `budget <corps> <pct>` sets one, `budget <corps> off` drops
// it. The Main Loop enforces them (core.Tick); core.Usage does the accounting.
func cmdBudget(e *Env, args []string, io IO) error {
	const usage = "usage: berthier budget [<corps> <pct>|off]"
	switch len(args) {
	case 0:
	case 2:
		corps := core.CorpsName(args[0])
		if corps == "" {
			return die("%s", usage)
		}
		var pct *float64
		if args[1] != "off" {
			v, err := strconv.ParseFloat(args[1], 64)
			if err != nil || strings.ContainsAny(args[1], "xX_pP") {
				return die("pct must be a number in (0, 100], or off: %s", usage)
			}
			pct = &v
		}
		if err := e.SetBudget(corps, pct); err != nil {
			return err
		}
		if pct == nil {
			fmt.Fprintf(io.Out, "budget for %s dropped\n", corps)
		} else {
			fmt.Fprintf(io.Out, "budget for %s: %s%% of the week\n", corps, args[1])
		}
		return nil
	default:
		return die("%s", usage)
	}

	u, err := e.UpdateUsage()
	if err != nil {
		return die("could not read the quota ledger")
	}
	v := u.View(e.ReadBudgets(), e.now())
	pct := func(f *float64) string {
		if f == nil {
			return "-"
		}
		return core.Num(roundTo(*f, 1)) + "%"
	}
	if v.WeekPct == nil {
		fmt.Fprintln(io.Out, "no quota samples yet: wire `berthier sample` into the status line")
	} else {
		when := "at an unknown time"
		if v.ResetsAt != nil {
			when = time.Unix(int64(*v.ResetsAt), 0).Format("Mon 02 Jan 15:04")
		}
		fmt.Fprintf(io.Out, "week resets %s, account at %s\n", when, pct(v.WeekPct))
	}
	rows := [][]string{{"corps", "used", "budget", "of budget"}}
	for _, c := range v.Corps {
		used := c.Used
		of := "-"
		if c.Ratio != nil {
			of = fmt.Sprintf("%d%%", int64(roundTo(*c.Ratio*100, 0)))
		}
		rows = append(rows, []string{c.Name, pct(&used), pct(c.Allowance), of})
	}
	columns(io, rows)
	return nil
}

func roundTo(f float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}
	return float64(int64(f*p+copySign(0.5, f))) / p
}

func copySign(a, b float64) float64 {
	if b < 0 {
		return -a
	}
	return a
}

// columns prints rows aligned like column(1): two spaces between columns, the
// last one unpadded.
func columns(io IO, rows [][]string) {
	w := map[int]int{}
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], utf8.RuneCountInString(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", w[i]-utf8.RuneCountInString(c)))
			}
		}
		fmt.Fprintln(io.Out, b.String())
	}
}

// --- ask

var wsRun = regexp.MustCompile(`\s+`)

func tidy(s string) string { return strings.TrimSpace(wsRun.ReplaceAllString(s, " ")) }

// cmdAsk sends a request to another Corps (or a Division, or berthier). The
// Main Loop delivers it to that Corps's Agent once the Agent is ready for input.
func cmdAsk(e *Env, args []string, io IO) error {
	if len(args) != 2 || args[1] == "" {
		return die(`usage: berthier ask <corps> "<what you need and why>"`)
	}
	pane := e.getenv("HERDR_PANE_ID")
	if pane == "" {
		return die("run this from an Agent's Herdr pane (no HERDR_PANE_ID)")
	}
	if st, err := os.Stat(e.State); err != nil || st.Size() == 0 {
		return die("no Grand Army State yet; is berthier-loop running?")
	}
	st := e.ReadState()
	to := core.CorpsName(args[0])
	known := map[string]bool{}
	for _, k := range st.Agents.Keys {
		a := st.Agents.M[k]
		if a.Status == "gone" {
			continue
		}
		if st.Berthier != nil && k == *st.Berthier {
			known["berthier"] = true
		} else {
			known[core.CorpsName(strOr(a.WorkspaceLabel))] = true
		}
	}
	if !known[to] {
		return die("no Corps '%s'; known: %s", to, knownList(sortedKeys(known)))
	}
	from := ""
	if st.Berthier != nil && *st.Berthier == pane {
		from = "berthier"
	} else if a := st.Agents.Get(pane); a != nil && a.WorkspaceLabel != nil {
		from = core.CorpsName(*a.WorkspaceLabel)
	}
	if from == "" {
		return die("pane %s is not a known Agent yet; try again in a few seconds", pane)
	}
	id := strings.ReplaceAll(fmt.Sprintf("%d-%s", e.Now().UnixNano(), from), "/", "-")
	body, _ := json.Marshal(struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
		At   int64  `json:"at"`
	}{from, to, tidy(args[1]), e.now()})
	if err := writeAtomic(filepath.Join(e.Inbox, id+".json"), append(body, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(io.Out, "asked %s; delivered when its Agent is ready for input\n", to)
	return nil
}

// --- notify

// cmdNotify is a one-way message to a Corps's Main Agent from something that is
// not an Agent (a systemd service). Same inbox as `ask`, kind "notify": no
// reply expected, never counted for ask-stuck or ping-pong. Needs no Herdr pane.
func cmdNotify(e *Env, args []string, io IO) error {
	if len(args) != 2 || args[1] == "" {
		return die(`usage: berthier notify <corps> "<text>"`)
	}
	if st, err := os.Stat(e.State); err != nil || st.Size() == 0 {
		return die("no Grand Army State yet; is berthier-loop running?")
	}
	to := core.CorpsName(args[0])
	st := e.ReadState()
	known := false
	for _, k := range st.Agents.Keys {
		a := st.Agents.M[k]
		if a.Status == "gone" {
			continue
		}
		if st.Berthier != nil && k == *st.Berthier {
			known = known || to == "berthier"
		} else {
			known = known || core.CorpsName(strOr(a.WorkspaceLabel)) == to
		}
	}
	if !known {
		return die("no Corps '%s'", to)
	}
	max := 2000
	if n, err := strconv.Atoi(e.getenv("BERTHIER_NOTIFY_MAX")); err == nil {
		max = n
	}
	text := []rune(args[1])
	body := string(text)
	if len(text) > max {
		body = string(text[:max]) + " [truncated]"
	}
	b, _ := json.Marshal(struct {
		Kind string `json:"kind"`
		From string `json:"from"`
		To   string `json:"to"`
		At   int64  `json:"at"`
		Text string `json:"text"`
	}{"notify", "service", to, e.now(), body})
	if err := writeAtomic(filepath.Join(e.Inbox, fmt.Sprintf("%d-service.json", e.Now().UnixNano())), append(b, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(io.Out, "notified %s; delivered when its Agent is ready for input\n", to)
	return nil
}

// --- report

// forkedFrom lists the sessions a backgrounded ("bg") Claude session sid was
// forked from, nearest first, found by walking up the process tree from pid.
// Herdr knows a pane by the session it started with, so a fork's Report is
// filed under those too; the Console's chat follows the same chain the other way.
func (e *Env) forkedFrom(sid string, pid int) []string {
	var ids []string
	kind := ""
	for hops := 0; pid > 1 && hops < 64; hops++ {
		var s struct {
			SessionID string `json:"sessionId"`
			Kind      string `json:"kind"`
		}
		if readJSON(filepath.Join(e.ClaudeDir, "sessions", fmt.Sprint(pid)+".json"), &s) {
			if kind == "" {
				if s.SessionID == sid {
					kind = s.Kind
				}
			} else {
				if kind != "bg" {
					break
				}
				ids = append(ids, s.SessionID)
				kind = s.Kind
			}
		}
		pid = parentPid(pid)
	}
	return ids
}

func parentPid(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// pid (comm) state ppid ...: comm may hold spaces and parens.
	s := string(b)
	f := strings.Fields(s[strings.LastIndex(s, ")")+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

// cmdReport is an Agent's one-line status for the user, shown in the bar's
// tooltip. Start the line with "needs you:" when the user must act. Outside a
// Herdr Claude session this is a no-op, so the global rule is safe everywhere.
func cmdReport(e *Env, args []string, io IO) error {
	if len(args) != 1 || args[0] == "" {
		return die(`usage: berthier report "<one line: what you are doing, or needs you: ...>"`)
	}
	sid, pane := e.getenv("CLAUDE_CODE_SESSION_ID"), e.getenv("HERDR_PANE_ID")
	if pane == "" || sid == "" {
		return nil
	}
	line := []rune(tidy(args[0]))
	if len(line) > 200 {
		line = line[:200]
	}
	body, _ := json.Marshal(struct {
		Line string `json:"line"`
		Pane string `json:"pane"`
		At   int64  `json:"at"`
	}{string(line), pane, e.now()})
	body = append(body, '\n')
	for _, id := range e.forkedFrom(sid, os.Getppid()) {
		if err := writeAtomic(filepath.Join(e.Reports, id+".json"), body); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(e.Reports, sid+".json"), body)
}

// cmdHookStop is a Claude Code Stop hook: an Agent in Herdr may not end a turn
// without having run `berthier report` during it. Reads the hook's JSON on stdin.
func cmdHookStop(e *Env, args []string, io IO) error {
	var in struct {
		StopHookActive bool   `json:"stop_hook_active"`
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	json.NewDecoder(io.In).Decode(&in)
	if e.getenv("HERDR_PANE_ID") == "" || in.StopHookActive || in.SessionID == "" {
		return nil
	}
	if st, err := os.Stat(in.TranscriptPath); err != nil || !st.Mode().IsRegular() {
		return nil
	}
	start, ok := turnStart(in.TranscriptPath)
	if !ok {
		return nil
	}
	var rep core.Report
	readJSON(filepath.Join(e.Reports, in.SessionID+".json"), &rep)
	if rep.At != nil && *rep.At >= start {
		return nil
	}
	fmt.Fprintln(io.Out, `{"decision":"block","reason":"Before stopping, run: berthier report \"<one line for the user: what you did or are doing; start with needs you: if they must act>\""}`)
	return nil
}

// turnStart is when the turn began: the last real user message (not a tool result).
func turnStart(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(f, 1<<20))
	last := ""
	for dec.More() {
		var m struct {
			Type      string `json:"type"`
			IsMeta    bool   `json:"isMeta"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if dec.Decode(&m) != nil {
			return 0, false
		}
		if m.Type == "user" && !m.IsMeta && realContent(m.Message.Content) {
			last = m.Timestamp
		}
	}
	t, err := time.Parse(time.RFC3339, last)
	return t.Unix(), err == nil
}

func realContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		return true
	}
	var parts []struct{ Type string }
	if json.Unmarshal(raw, &parts) != nil {
		return false
	}
	for _, p := range parts {
		if p.Type == "text" {
			return true
		}
	}
	return false
}

func cmdBriefAfterDialog(e *Env, args []string, io IO) error {
	if len(args) != 3 {
		return die("usage: berthier brief-after-dialog <name> <pane> <brief>")
	}
	e.BriefAfterDialog(args[0], args[1], args[2])
	return nil
}

// --- corps-new, adopt, division-new, division-done

func (e *Env) hasWorkspace(label string) (bool, error) {
	ws, err := e.Workspaces()
	for _, w := range ws {
		if w.Label == label {
			return true, err
		}
	}
	return false, err
}

// cmdCorpsNew creates a Corps: a Herdr workspace labelled <name> at <dir>,
// with a Main Agent started there and given its Brief. A brand-new directory
// becomes a git repo so the Main Agent can spawn Divisions from it.
func cmdCorpsNew(e *Env, args []string, io IO) error {
	if len(args) < 2 {
		return die("usage: berthier corps-new <name> <dir> [brief]")
	}
	name, dir, brief := args[0], args[1], ""
	if len(args) > 2 {
		brief = args[2]
	}
	if name == "berthier" {
		return die("reserved corps name: %s", name)
	}
	if ok, _ := e.hasWorkspace(name); ok {
		return die("a workspace labelled '%s' already exists", name)
	}
	dir, _ = filepath.Abs(dir)
	if _, err := os.Lstat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		exec.Command("git", "-C", dir, "init", "-q").Run()
		exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "Initial commit").Run()
	}
	var r struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		}
	}
	if err := e.H.JSON(&r, "workspace", "create", "--cwd", dir, "--label", name, "--no-focus"); err != nil || r.Result.RootPane.PaneID == "" {
		return die("could not create workspace '%s'", name)
	}
	pane := r.Result.RootPane.PaneID
	agent := core.Slug(name)
	switch e.StartClaude(agent, pane, brief, "", func(s string) { fmt.Fprintln(io.Err, s) }) {
	case "ready":
		fmt.Fprintf(io.Out, "corps '%s' ready: workspace pane %s, Main Agent '%s'\n", name, pane, agent)
	case "blocked":
		fmt.Fprintf(io.Out, "corps '%s' created: pane %s, Main Agent '%s' is waiting on a startup dialog (likely folder trust) - the user must answer it in Herdr\n", name, pane, agent)
	default:
		return die("corps '%s' created (pane %s) but its Main Agent did not start", name, pane)
	}
	return nil
}

// cmdAdopt makes a workspace a Division of a Corps: record its Corps in
// state/divisions.json; the label stays. Its Agents keep running; the Corps's
// Main Agent now commands them, and the Main Loop sends their trouble there
// instead of to Berthier.
func cmdAdopt(e *Env, args []string, io IO) error {
	if len(args) != 2 {
		return die("usage: berthier adopt <workspace> <corps>")
	}
	child, corps := args[0], args[1]
	if child == corps {
		return die("a Corps cannot adopt itself")
	}
	ws, err := e.Workspaces()
	if err != nil {
		return err
	}
	has := func(l string) bool {
		for _, w := range ws {
			if w.Label == l {
				return true
			}
		}
		return false
	}
	if !has(child) {
		return die("no workspace labelled '%s'", child)
	}
	if !has(corps) {
		return die("no Corps '%s'", corps)
	}
	divs := e.ReadDivisions()
	if strings.Contains(child, "/") || divs.IsDivision(child) {
		return die("'%s' is already a Division", child)
	}
	if child == "berthier" {
		return die("berthier answers to the user only")
	}
	if strings.Contains(corps, "/") || divs.IsDivision(corps) {
		return die("'%s' is a Division, not a Corps", corps)
	}
	if err := e.SetDivision(core.CorpsName(child), &core.Division{Corps: corps}); err != nil {
		return err
	}
	e.Event("adopted", fmt.Sprintf("adopted %s into %s", child, corps), core.Fields{"corps": core.CorpsName(corps), "division": core.CorpsName(child)})
	fmt.Fprintf(io.Out, "'%s' is now a Division of %s; brief %s's Main Agent that it commands it\n", child, corps, corps)
	return nil
}

func (e *Env) callerWorkspace() (id, label string, err error) {
	id = e.getenv("BERTHIER_WORKSPACE")
	if id == "" {
		id = e.getenv("HERDR_WORKSPACE_ID")
	}
	if id == "" {
		return "", "", die("run this from the Main Agent's Herdr pane (no HERDR_WORKSPACE_ID)")
	}
	var r struct {
		Result struct{ Workspace struct{ Label string } }
	}
	if err := e.H.JSON(&r, "workspace", "get", id); err != nil {
		return "", "", err
	}
	return id, r.Result.Workspace.Label, nil
}

// cmdDivisionNew is run by a Corps's Main Agent from its own pane: spawn a
// Division for a side feature, a worktree workspace labelled "<name>" on
// branch <name> (Herdr draws it under its Corps), with its own Agent started
// on the Brief. The Corps commands it.
func cmdDivisionNew(e *Env, args []string, io IO) error {
	const usage = "usage: berthier division-new <name> [--model <m>] <brief>"
	// --model may come anywhere; anything else beyond <name> <brief> is an error.
	model := e.DivisionModel
	var pos []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--model":
			if i+1 >= len(args) || args[i+1] == "" {
				return die("%s", usage)
			}
			model = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--"):
			return die("%s", usage)
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) != 2 {
		return die("%s", usage)
	}
	name, brief := pos[0], pos[1]
	wsID, corps, err := e.callerWorkspace()
	if err != nil {
		return err
	}
	if _, in := e.ReadDivisions()[core.CorpsName(corps)]; in {
		return die("already inside a Division; spawn from the Corps's own workspace")
	}
	// Herdr fills in workspace git metadata lazily, so ask git from a pane's cwd.
	var panes struct {
		Result struct{ Panes []struct{ Cwd string } }
	}
	if err := e.H.JSON(&panes, "pane", "list", "--workspace", wsID); err != nil || len(panes.Result.Panes) == 0 {
		return die("no pane in workspace %s", wsID)
	}
	out, gerr := exec.Command("git", "-C", panes.Result.Panes[0].Cwd, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if gerr != nil {
		return die("Corps '%s' is not a git repository; a new Division needs a worktree", corps)
	}
	repo := filepath.Dir(strings.TrimSpace(string(out)))
	branch := core.Slug(name)
	if branch == "" {
		return die("division name must contain letters")
	}
	if ok, _ := e.hasWorkspace(branch); ok {
		return die("a workspace labelled '%s' already exists; pick another name", branch)
	}
	var r struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		}
	}
	if err := e.H.JSON(&r, "worktree", "create", "--cwd", repo, "--branch", branch, "--label", branch, "--no-focus"); err != nil || r.Result.RootPane.PaneID == "" {
		return die("could not create worktree for branch '%s'", branch)
	}
	pane := r.Result.RootPane.PaneID
	if err := e.SetDivision(branch, &core.Division{Corps: corps, Worktree: true, Model: model}); err != nil {
		return err
	}
	agent := core.Slug(corps + "-" + branch)
	switch e.StartClaude(agent, pane, brief, model, func(s string) { fmt.Fprintln(io.Err, s) }) {
	case "ready":
		fmt.Fprintf(io.Out, "division '%s' of %s started: branch %s, pane %s, agent '%s'\n", branch, corps, branch, pane, agent)
	case "blocked":
		fmt.Fprintf(io.Out, "division '%s' of %s created: pane %s, agent '%s' is waiting on a startup dialog - the user must answer it in Herdr\n", branch, corps, pane, agent)
	default:
		return die("division '%s' created (pane %s) but its agent did not start", branch, pane)
	}
	return nil
}

// cmdDivisionDone is run by a Corps's Main Agent once the user has reviewed a
// Division it spawned: remove the worktree workspace, and delete the branch
// only if it is merged.
func cmdDivisionDone(e *Env, args []string, io IO) error {
	if len(args) != 1 {
		return die("usage: berthier division-done <name>")
	}
	branch := core.Slug(args[0])
	_, corps, err := e.callerWorkspace()
	if err != nil {
		return err
	}
	entry, ok := e.ReadDivisions()[branch]
	if !ok {
		return die("no Division '%s'", branch)
	}
	if entry.Corps != corps {
		return die("Division '%s' belongs to %s, not %s", branch, entry.Corps, corps)
	}
	if !entry.Worktree {
		return die("'%s' is an adopted workspace, not a spawned worktree; it stays until the user closes it", branch)
	}
	ws, err := e.Workspaces()
	if err != nil {
		return err
	}
	var div *core.Workspace
	for i := range ws {
		if ws[i].Label == branch {
			div = &ws[i]
			break
		}
	}
	if div == nil {
		return die("no workspace labelled '%s'", branch)
	}
	if _, err := e.H.Run("worktree", "remove", "--workspace", div.ID); err != nil {
		path := ""
		if div.Worktree != nil && div.Worktree.CheckoutPath != nil {
			path = *div.Worktree.CheckoutPath
		}
		return die("could not remove '%s' (uncommitted changes in %s?)", branch, path)
	}
	if err := e.SetDivision(branch, nil); err != nil {
		return err
	}
	repo := ""
	if div.Worktree != nil && div.Worktree.RepoRoot != nil {
		repo = *div.Worktree.RepoRoot
	}
	if exec.Command("git", "-C", repo, "branch", "-d", branch).Run() == nil {
		fmt.Fprintf(io.Out, "division '%s' done: worktree removed, merged branch %s deleted\n", branch, branch)
	} else {
		fmt.Fprintf(io.Out, "division '%s' done: worktree removed; branch %s kept because it is not merged\n", branch, branch)
	}
	return nil
}

// setBudgetFor is the Console's budget write: Corps names as `budget` takes them.
func (e *Env) setBudgetFor(corps string, pct *float64) error {
	corps = core.CorpsName(corps)
	if corps == "" {
		return BadBudget("no corps")
	}
	return e.SetBudget(corps, pct)
}

// cmdDashboard serves the Grand Army console on 127.0.0.1:8430. Flags:
// -addr, -every (view model refresh), -claude-dir.
func cmdDashboard(e *Env, args []string, io IO) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(io.Err)
	addr := fs.String("addr", "127.0.0.1:8430", "listen address")
	every := fs.Duration("every", 2*time.Second, "how often to refresh the view model")
	claude := fs.String("claude-dir", e.ClaudeDir, "Claude Code's directory (sessions and transcripts)")
	if err := fs.Parse(args); err != nil {
		return exitCode(2)
	}
	source := func() ([]byte, error) {
		var b bytes.Buffer
		err := encode(&b, e.ArmyView())
		return b.Bytes(), err
	}
	return console.Serve(*addr, source, e.H, e.setBudgetFor, *claude, *every)
}

// cmdPrompt prompts the Agent in a pane and makes sure Claude took it
// (herdr.Client.PromptAgent): a bare `herdr agent prompt` can leave a draft.
// Text "-" is read from stdin, for long or multi-line prompts.
func cmdPrompt(e *Env, args []string, stdio IO) error {
	text := ""
	if len(args) == 2 && args[1] == "-" {
		b, err := io.ReadAll(stdio.In)
		if err != nil {
			return err
		}
		text = strings.TrimRight(string(b), "\n")
	} else if len(args) >= 2 {
		text = strings.Join(args[1:], " ")
	}
	if strings.TrimSpace(text) == "" {
		return die(`usage: berthier prompt <pane> "<text>"|-`)
	}
	return e.H.PromptAgent(args[0], text)
}

// cmdLast prints the final assistant message of the Claude Agent in a pane,
// from its transcript (as the Console's chat reads it): no pane read, so the
// user's view never scrolls.
func cmdLast(e *Env, args []string, io IO) error {
	if len(args) != 1 {
		return die("usage: berthier last <pane>")
	}
	ag, err := console.GetAgent(e.H, args[0])
	if err != nil {
		return err
	}
	if ag.Kind != "claude" || ag.Session == "" {
		return die("%s is not a Claude Agent with a session", args[0])
	}
	tr := console.NewTranscripts(e.ClaudeDir)
	path := tr.Path(ag.Session)
	if path == "" {
		return die("no transcript for session %s", ag.Session)
	}
	msg, ok := tr.Last(path)
	if !ok {
		return die("no assistant message yet in %s", path)
	}
	if ag.Status == "working" {
		fmt.Fprintf(io.Err, "(%s is still working; this is its latest message)\n", args[0])
	}
	fmt.Fprintln(io.Out, strings.TrimSpace(msg.Text))
	return nil
}
