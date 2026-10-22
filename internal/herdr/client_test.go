package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude copies tests/fake-herdr, a Herdr whose one Claude takes prompts
// the given way, into a temp dir that also holds its state.
func fakeClaude(t *testing.T, mode string) (c Client, dir string) {
	src, err := os.ReadFile("../../tests/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	script := filepath.Join(dir, "herdr")
	os.WriteFile(script, src, 0o755)
	os.WriteFile(filepath.Join(dir, "mode"), []byte(mode), 0o644)
	return Client{Bin: script, PromptPoll: time.Millisecond}, dir
}

func TestPromptAgent(t *testing.T) {
	for _, c := range []struct{ mode, err, calls string }{
		{"takes", "", "agent prompt"},
		{"draft", "", "agent prompt,pane read,agent send-keys"},
		{"lost", "", "agent prompt,pane read,agent prompt"},
		{"busy", "", "agent prompt,pane read"},
		{"never", "Claude in p1 did not take the prompt",
			"agent prompt,pane read,agent send-keys,pane read,agent send-keys,pane read,agent send-keys,pane read"},
		{"error", "agent target x not found", "agent prompt"},
	} {
		client, dir := fakeClaude(t, c.mode)
		got := ""
		if err := client.PromptAgent("p1", "Look at /tmp/a.png and reply"); err != nil {
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

func TestSessionFlag(t *testing.T) {
	client, dir := fakeClaude(t, "takes")
	client.Session = "scratch"
	client.Run("agent", "get", "p1")
	raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.HasPrefix(string(raw), "--session scratch agent get p1") {
		t.Errorf("calls %q", raw)
	}
}
