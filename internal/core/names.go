// Package core holds Berthier's pure decisions: no I/O, no clock, no
// environment. Everything it needs comes in as arguments (ADR 0001, 0004).
package core

import (
	"regexp"
	"strconv"
	"strings"
)

// CorpsName is how an Agent is addressed in `berthier ask`: its Corps
// ("dotfiles") or its Division ("dark-mode"). A leading dot and case drop.
func CorpsName(s string) string {
	return strings.ToLower(strings.TrimPrefix(s, "."))
}

// Division is a record of state/divisions.json: the Corps a workspace belongs
// to, whether berthier spawned its worktree, the model it runs on, and whether
// its Agent may use Artifact (off unless division-new --artifact).
type Division struct {
	Corps    string `json:"corps"`
	Worktree bool   `json:"worktree"`
	Model    string `json:"model,omitempty"`
	Artifact bool   `json:"artifact,omitempty"`
}

// Divisions maps CorpsName(label) to its record.
type Divisions map[string]Division

// IsDivision reports whether the workspace label is a recorded Division.
func (d Divisions) IsDivision(label string) bool {
	_, ok := d[CorpsName(label)]
	return ok
}

// Owner is the Corps a workspace label counts toward: a Division's Corps,
// else itself.
func (d Divisions) Owner(label string) string {
	n := CorpsName(label)
	if r, ok := d[n]; ok && r.Corps != "" {
		return CorpsName(r.Corps)
	}
	return CorpsName(n)
}

var slugBad = regexp.MustCompile(`[^a-z0-9_-]+`)

// Slug makes a Herdr agent name ([a-z][a-z0-9_-]{0,31}) from free text.
func Slug(s string) string {
	s = slugBad.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.TrimLeftFunc(s, func(r rune) bool { return r < 'a' || r > 'z' })
	s = strings.TrimRight(s, "-")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

// Spoken is how a workspace label reads aloud: "dark-mode" -> "dark mode".
func Spoken(s string) string {
	if s == "" {
		s = "an agent"
	}
	return strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimPrefix(s, "."))
}

// FallbackName names an agent Herdr has not named, after its pane.
func FallbackName(pane string) string {
	return "a-" + regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(strings.ToLower(pane), "-")
}

// Num formats a number the way jq prints it.
func Num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
