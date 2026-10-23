package core

import (
	"path/filepath"
	"strings"
)

// Overhead is the name Berthier's own Spend goes by: Command overhead, a line
// of its own rather than part of the Corps whose workspace Berthier lives in.
const Overhead = "berthier"

// Place is where a working directory counts: its Corps and, in a Division, the
// Division's name.
type Place struct {
	Path     string `json:"path"`
	Corps    string `json:"corps"`
	Division string `json:"division,omitempty"`
}

// Placer maps a session's cwd to its Corps and Division, from the Grand Army
// State. The longest matching path wins, so a Division's worktree beats its
// repo. A finished Division's worktree (gone from the state) is still placed
// by its layout: Herdr puts worktrees at <...>/.herdr/worktrees/<repo>/<name>,
// Claude Code at <repo>/.claude/worktrees/<name>.
type Placer struct {
	Places []Place
	Repos  map[string]string // repository directory name -> Corps
}

// NewPlacer lists where each Corps, Division and Berthier work. root is
// Berthier's repository: its sessions are Command overhead, while its
// Divisions count toward the Corps of Berthier's workspace.
func NewPlacer(s *State, d Divisions, root string) Placer {
	b := "berthier"
	if a := s.Agents.Get(deref(s.Berthier)); a != nil && a.WorkspaceLabel != nil {
		b = CorpsName(*a.WorkspaceLabel)
	}
	p := Placer{Repos: map[string]string{}}
	add := func(path, corps, div string) {
		if path != "" && path != "/" {
			p.Places = append(p.Places, Place{path, corps, div})
		}
	}
	for _, k := range s.Agents.Keys {
		a := s.Agents.M[k]
		if a.Cwd == nil || k == deref(s.Berthier) {
			continue
		}
		label := deref(a.WorkspaceLabel)
		if d.IsDivision(label) {
			add(*a.Cwd, d.Owner(label), CorpsName(label))
		} else {
			add(*a.Cwd, CorpsName(label), "")
		}
	}
	for _, c := range s.Corps {
		n := CorpsName(c.Name)
		if c.RepoRoot != nil {
			add(*c.RepoRoot, n, "")
			p.Repos[filepath.Base(*c.RepoRoot)] = n
		}
		for _, dv := range c.Divisions {
			add(deref(dv.Path), n, CorpsName(dv.Name))
		}
	}
	for _, dv := range s.BerthierDivisions {
		add(deref(dv.Path), b, CorpsName(dv.Name))
	}
	if root != "" {
		add(root, Overhead, "")
		p.Repos[filepath.Base(root)] = b
	}
	return p
}

// Place is where cwd counts; ok is false for a cwd no Corps works in.
func (p Placer) Place(cwd string) (corps, division string, ok bool) {
	best := -1
	for i, e := range p.Places {
		if (cwd == e.Path || strings.HasPrefix(cwd, e.Path+"/")) && (best < 0 || len(e.Path) >= len(p.Places[best].Path)) {
			best = i
		}
	}
	if best >= 0 {
		corps, division = p.Places[best].Corps, p.Places[best].Division
	}
	if i := strings.Index(cwd, "/.herdr/worktrees/"); i >= 0 {
		parts := strings.Split(cwd[i+len("/.herdr/worktrees/"):], "/")
		if c, known := p.Repos[parts[0]]; known && len(parts) > 1 && division == "" {
			return c, CorpsName(parts[1]), true
		}
	}
	if i := strings.Index(cwd, "/.claude/worktrees/"); i >= 0 && best >= 0 && division == "" {
		name := strings.Split(cwd[i+len("/.claude/worktrees/"):], "/")[0]
		if corps == Overhead {
			corps = p.Repos[filepath.Base(cwd[:i])]
		}
		return corps, CorpsName(name), true
	}
	return corps, division, best >= 0
}
