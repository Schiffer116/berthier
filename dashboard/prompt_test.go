package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude copies tests/fake-herdr, a Herdr whose one Claude takes prompts
// the given way, into a temp dir that also holds its state.
func fakeClaude(t *testing.T, mode string) (script, dir string) {
	src, err := os.ReadFile("../tests/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	script = filepath.Join(dir, "herdr")
	os.WriteFile(script, src, 0o755)
	os.WriteFile(filepath.Join(dir, "mode"), []byte(mode), 0o644)
	poll := PromptPoll
	PromptPoll = time.Millisecond
	t.Cleanup(func() { PromptPoll = poll })
	return script, dir
}

func TestPromptAgent(t *testing.T) {
	t.Setenv("BERTHIER_HERDR_SESSION", "")
	for _, c := range []struct{ mode, err, calls string }{
		{"takes", "", "agent prompt"},
		{"draft", "", "agent prompt,pane read,agent send-keys"},
		{"lost", "", "agent prompt,pane read,agent prompt"},
		{"busy", "", "agent prompt,pane read"},
		{"never", "Claude in p1 did not take the prompt",
			"agent prompt,pane read,agent send-keys,pane read,agent send-keys,pane read,agent send-keys,pane read"},
		{"error", "agent target x not found", "agent prompt"},
	} {
		herdr, dir := fakeClaude(t, c.mode)
		got := ""
		if err := PromptAgent(herdr, "p1", "Look at /tmp/a.png and reply"); err != nil {
			got = err.Error()
		}
		if got != c.err {
			t.Errorf("%s: error %q, want %q", c.mode, got, c.err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
		var calls []string
		for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if f := strings.Fields(l); len(f) >= 2 && f[1] != "get" {
				calls = append(calls, f[0]+" "+f[1])
			}
		}
		if got := strings.Join(calls, ","); got != c.calls {
			t.Errorf("%s: calls %s, want %s", c.mode, got, c.calls)
		}
	}
}

func TestInputHolds(t *testing.T) {
	rule := strings.Repeat("─", 50)
	box := func(lines ...string) string {
		return "● Done.\n" + rule + "\n" + strings.Join(lines, "\n") + "\n" + rule + "\n  5h: 87% | Cost: $0.00"
	}
	ask := "[berthier ask] from ui-tweaks: PR #20 board header. Screenshots: /tmp/a.png /tmp/b.png. Same owner rule as list."
	for _, c := range []struct {
		name, tail, text string
		want             bool
	}{
		{"image draft", box("❯ [Image #4] [Image #5][berthier ask] from ui-tweaks: PR #20 board", "  header. Screenshots: . Same owner rule as list."), ask, true},
		{"wrapped draft", box("❯ Reply with just:", "  pong, then stop"), "Reply with just: pong, then stop", true},
		{"long paste", box("❯ [Pasted text #1 +40 lines]"), ask, true},
		{"empty", box("❯"), ask, false},
		{"suggestion", box("❯ commit this"), ask, false},
		{"no box", "● working", ask, false},
	} {
		if got := inputHolds(c.tail, c.text); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
