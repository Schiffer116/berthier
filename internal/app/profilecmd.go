package app

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"berthier/internal/core"
)

// ProfileOpts selects what `berthier profile` shows.
type ProfileOpts struct {
	WeeksAgo int
	Corps    string
	Session  string
	WasteMax int
}

// Profile folds new transcript lines and computes the Profile view.
func (e *Env) Profile(o ProfileOpts) (core.ProfileView, error) {
	if err := e.UpdateProfile(); err != nil {
		return core.ProfileView{}, err
	}
	u, err := e.UpdateUsage()
	if err != nil {
		return core.ProfileView{}, err
	}
	return e.profileView(u, o), nil
}

// profileView computes the view from the index as it is, without folding.
func (e *Env) profileView(u core.Usage, o ProfileOpts) core.ProfileView {
	now := float64(e.now())
	since := core.WeekStart(now, u.ResetsAt) - float64(e.ProfileWeeks-1)*core.Week
	v := u.View(nil, int64(now))
	in := core.ProfileIn{Turns: e.LoadTurns(), Archive: e.ReadArchive(), Landed: e.Landed(since), Now: now,
		ResetsAt: u.ResetsAt, WeekPct: v.WeekPct, ByCorps: map[string]float64{},
		WeeksAgo: o.WeeksAgo, Corps: core.CorpsName(o.Corps), Session: o.Session, WasteMax: o.WasteMax}
	for _, c := range v.Corps {
		in.ByCorps[c.Name] = c.Used
	}
	return core.Profile(in)
}

// cmdProfile prints the Profile view as JSON: Spend of a quota week by Corps,
// Division and session, with Spend per Delivered unit and ranked Waste
// signals. --waste prints only the ranked Waste signals (all of them).
func cmdProfile(e *Env, args []string, io IO) error {
	const usage = "usage: berthier profile [--waste] [--corps <corps>] [--session <id>] [--week <weeks ago>]"
	var o ProfileOpts
	waste := false
	for i := 0; i < len(args); i++ {
		val := func() (string, error) {
			if i+1 >= len(args) || args[i+1] == "" {
				return "", die("%s", usage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--waste":
			waste = true
		case "--corps":
			o.Corps, err = val()
		case "--session":
			o.Session, err = val()
		case "--week":
			var s string
			if s, err = val(); err == nil {
				if o.WeeksAgo, err = strconv.Atoi(s); err != nil || o.WeeksAgo < 0 {
					return die("%s", usage)
				}
			}
		default:
			return die("%s", usage)
		}
		if err != nil {
			return err
		}
	}
	if waste {
		o.WasteMax = 1 << 30
	}
	v, err := e.Profile(o)
	if err != nil {
		return err
	}
	if waste {
		return encode(io.Out, struct {
			Week  core.WeekSpan    `json:"week"`
			Waste []core.WasteItem `json:"waste"`
		}{v.Week, v.Waste})
	}
	return encode(io.Out, v)
}

// --- Delivered units from git

var mergeBranch = regexp.MustCompile(`^Merge branch '([^']+)'`)

type landedKey struct {
	root, head string
	since      float64
}

var landedCache sync.Map // landedKey -> []core.Landed

// Landed lists the Delivered units since a time in every Corps's repository:
// commits on the branch its Main Agent works (HEAD at the repo root), each
// Division merge with the commits it brought. Berthier's own commits are
// Command overhead.
func (e *Env) Landed(since float64) []core.Landed {
	st := e.ReadState()
	b := "berthier"
	if a := st.Agents.Get(strOr(st.Berthier)); a != nil && a.WorkspaceLabel != nil {
		b = core.CorpsName(*a.WorkspaceLabel)
	}
	type repo struct{ root, corps, main string }
	repos := []repo{}
	for _, c := range st.Corps {
		if c.RepoRoot != nil {
			n := core.CorpsName(c.Name)
			repos = append(repos, repo{*c.RepoRoot, n, n})
		}
	}
	if e.Root != "" {
		repos = append(repos, repo{e.Root, b, core.Overhead})
	}
	out := []core.Landed{}
	for _, r := range repos {
		out = append(out, landedIn(r.root, r.corps, r.main, since)...)
	}
	return out
}

func landedIn(root, corps, main string, since float64) []core.Landed {
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return nil
	}
	k := landedKey{root, strings.TrimSpace(string(head)), since}
	if v, ok := landedCache.Load(k); ok {
		return v.([]core.Landed)
	}
	out, err := exec.Command("git", "-C", root, "log", "--first-parent", fmt.Sprintf("--since=@%d", int64(since)),
		"--format=%H%x1f%ct%x1f%P%x1f%s", "HEAD").Output()
	if err != nil {
		return nil
	}
	var l []core.Landed
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 4 {
			continue
		}
		at, _ := strconv.ParseFloat(f[1], 64)
		parents := strings.Fields(f[2])
		x := core.Landed{Corps: main, Commits: 1, At: at, Subject: f[3]}
		if len(parents) == 2 {
			n, err := exec.Command("git", "-C", root, "rev-list", "--count", parents[0]+".."+parents[1]).Output()
			c, _ := strconv.Atoi(strings.TrimSpace(string(n)))
			if m := mergeBranch.FindStringSubmatch(f[3]); m != nil && err == nil {
				x = core.Landed{Corps: corps, Division: core.CorpsName(m[1]), Commits: c, Finished: true, At: at, Subject: f[3]}
			} else if err == nil {
				x.Commits = max(c, 1)
			}
		}
		l = append(l, x)
	}
	landedCache.Store(k, l)
	return l
}
