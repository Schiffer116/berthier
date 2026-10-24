package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Frozen Agents: a Revive (or a usage-limit resume) resumes the session, and
// with its prompt cache gone cold that rewrites its whole context. The Main
// Loop does it by itself only while the cache is warm or the context small;
// otherwise the Agent is Frozen until the user chooses Revive or Fresh Start.

// Cache is what a session's transcript says about its prompt cache: its last
// API call, the context the next call sends (Context tokens), and the model.
type Cache struct {
	LastAt  int64  `json:"last_at"`
	Context int64  `json:"context"`
	Model   string `json:"model,omitempty"`
	TTL1h   bool   `json:"ttl_1h,omitempty"`
}

// Frozen is an Agent due a Revive (Why "crash") or a usage-limit resume
// ("limit") whose cache went cold: nothing is resumed or delivered to it until
// the user chooses.
type Frozen struct {
	Since int64  `json:"since"`
	Why   string `json:"why"`
	Cache
}

// Warm: resuming now reads the cache, or the context is too small to matter.
func (c Cache) Warm(now int64, cfg Config) bool {
	return now-c.LastAt < cfg.CacheWarm || c.Context < cfg.FrozenMin
}

// ReviveUSD estimates what resuming a cold session costs: its whole context
// written to the cache again (at the TTL Claude Code used for it).
func (c Cache) ReviveUSD() (float64, bool) { return c.WriteUSD(c.Context) }

// WriteUSD prices n tokens written to the cache, as ReviveUSD does the context.
func (c Cache) WriteUSD(n int64) (float64, bool) {
	p, ok := PriceOf(c.Model)
	if !ok {
		return 0, false
	}
	t := Tokens{W5m: n}
	if c.TTL1h {
		t = Tokens{W1h: n}
	}
	return p.USD(t, false), true
}

// CacheOf reads a transcript's last lines for its last real API call
// (Claude's own synthetic messages, such as a usage-limit stop, call nothing).
func CacheOf(lines []string) (Cache, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"usage"`) {
			continue
		}
		var l tLine
		if json.Unmarshal([]byte(lines[i]), &l) != nil || l.Type != "assistant" || l.Message.Usage == nil ||
			l.Message.Model == "" || strings.HasPrefix(l.Message.Model, "<") {
			continue
		}
		u := l.Message.Usage
		if u.In+u.CacheRead+u.CacheCreation == 0 {
			continue
		}
		c := Cache{LastAt: int64(parseTS(l.Timestamp)), Context: u.In + u.CacheRead + u.CacheCreation + u.Out, Model: l.Message.Model}
		// Claude Code writes with a 1-hour TTL unless the split says otherwise.
		c.TTL1h = u.Split == nil || u.Split.W1h > 0 || u.Split.W5m == 0
		return c, true
	}
	return Cache{}, false
}

// FreshPrompt is the first prompt of a Fresh Start: rebuild from what is
// written down. A Division also gets its Brief again.
func FreshPrompt(report, brief string) string {
	p := "[berthier fresh] You were restarted fresh: run berthier notes, read your Board, your last report and git status, then continue."
	if report != "" {
		p += fmt.Sprintf("\nYour last report: %s", report)
	}
	if brief != "" {
		p += "\nYour branch holds your progress so far. Your original Brief:\n\n" + brief
	}
	return p
}

// Thaw is the user's choice for a Frozen Agent (`berthier revive|fresh`, the
// Console): How is "revive" or "fresh".
type Thaw struct {
	ID   string `json:"id"`
	Pane string `json:"pane"`
	How  string `json:"how"`
	At   int64  `json:"at"`
}

// FreshStart replaces a Frozen Agent's session with a new one in its pane.
// Live: its Claude still runs (stopped on its usage limit) and must exit first.
type FreshStart struct {
	PaneID   string `json:"pane_id"`
	Name     string `json:"name"`
	Session  string `json:"session"` // the frozen session, whose last report the prompt carries
	Model    string `json:"model,omitempty"`
	Division string `json:"division,omitempty"` // its Division, whose Brief it gets again
	Label    string `json:"label,omitempty"`    // its workspace label: decides its launch flags
	Live     bool   `json:"live,omitempty"`
	Ev       Fields `json:"ev"`
}

// tokensK is a token count for people: 183k.
func tokensK(n int64) string { return fmt.Sprintf("%dk", (n+500)/1000) }
