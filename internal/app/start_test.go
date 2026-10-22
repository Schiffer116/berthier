package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr installs tests/fake-herdr, a Herdr with one Claude Agent whose
// `mode` says how it takes a prompt, and points the sandbox at it.
func fakeHerdr(t *testing.T, s *sandbox) (dir string) {
	src, err := os.ReadFile("../../tests/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "herdr"), src, 0o755)
	s.env["BERTHIER_HERDR"] = filepath.Join(dir, "herdr")
	return dir
}

func (s *sandbox) mode(dir, m string) {
	os.WriteFile(filepath.Join(dir, "mode"), []byte(m), 0o644)
	for _, f := range []string{"seq", "box", "calls", "text", "answered"} {
		os.Remove(filepath.Join(dir, f))
	}
}

func calls(dir string) string {
	raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	var l []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && !(f[0] == "agent" && f[1] == "get") {
			l = append(l, f[0]+" "+f[1])
		}
	}
	return strings.Join(l, ",")
}

func TestStartClaude(t *testing.T) {
	s := newSandbox(t)
	dir := fakeHerdr(t, s)
	e := LoadEnv(s.getenv)
	brief := "Build it.\nThen report."
	start := func(mode, brief string) (string, string) {
		s.mode(dir, mode)
		var warn []string
		got := e.StartClaude("demo", "p1", brief, "", func(w string) { warn = append(warn, w) })
		return got, strings.Join(warn, "|")
	}

	got, _ := start("takes", brief)
	eq(t, "brief sent as a prompt", got+" "+calls(dir), "ready agent start,agent prompt")
	raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	eq(t, "start has no brief argument", strings.SplitN(string(raw), "\n", 2)[0], "agent start demo --kind claude --pane p1 --timeout 45000")
	got, warn := start("nostart", brief)
	eq(t, "start failure says why", got+" "+warn, "failed agent arguments cannot be encoded safely for the target shell")
	got, warn = start("never", brief)
	eq(t, "undelivered brief fails", got+" "+warn, "failed Brief not delivered: Claude in p1 did not take the prompt")
}

func TestBriefAfterDialog(t *testing.T) {
	s := newSandbox(t)
	dir := fakeHerdr(t, s)
	e := LoadEnv(s.getenv)
	s.mode(dir, "dialog")
	e.BriefAfterDialog("demo", "p1", "Build it.\nThen report.")
	line := s.read("loop.log")
	eq(t, "brief sent after the dialog", jq(t, strings.TrimSpace(line), "kind")+jq(t, strings.TrimSpace(line), "pane")+jq(t, strings.TrimSpace(line), "name"), `"brief_sent""p1""demo"`)
	text, _ := os.ReadFile(filepath.Join(dir, "text"))
	eq(t, "brief prompted", string(text), "Build it.\nThen report.")
}
