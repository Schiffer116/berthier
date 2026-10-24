package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"berthier/internal/core"
)

func TestFreshStart(t *testing.T) {
	s := newSandbox(t)
	dir := fakeHerdr(t, s)
	e := LoadEnv(s.getenv)
	s.write("reports/old.json", `{"line":"halfway through the parser","pane":"p1","at":1}`)
	if err := e.saveBrief("dark-mode", "Build dark mode."); err != nil {
		t.Fatal(err)
	}
	f := core.FreshStart{PaneID: "p1", Name: "skipli-dark-mode", Session: "old", Model: "claude-sonnet-5-5", Division: "dark-mode", Live: true, Ev: core.Fields{"pane": "p1"}}

	s.mode(dir, "takes")
	os.WriteFile(filepath.Join(dir, "shell"), nil, 0o644)
	e.freshStart(f)
	eq(t, "a live Claude is ended, then a new session started and prompted", calls(dir), "pane send-keys,pane send-keys,pane process-info,pane process-info,agent start,pane read,agent prompt")
	raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	eq(t, "same model", strings.Contains(string(raw), "agent start skipli-dark-mode --kind claude --pane p1 --timeout 45000 -- --model claude-sonnet-5-5"), true)
	eq(t, "prompt", readFile(filepath.Join(dir, "text")), core.FreshPrompt("halfway through the parser", "Build dark mode."))
	eq(t, "logged", strings.Contains(s.read("loop.log"), `"kind":"fresh_started"`), true)

	s.mode(dir, "takes")
	os.Remove(filepath.Join(dir, "shell"))
	f.Live = false
	e.freshStart(f)
	eq(t, "no shell: failed, the user told", []any{calls(dir), strings.Contains(s.read("loop.log"), `"kind":"fresh_failed"`)}, "[pane process-info,notification show true]")
}

func TestThawRequests(t *testing.T) {
	s := newSandbox(t)
	e := LoadEnv(s.getenv)
	s.write("state.json", `{"agents":{"w1:p1":{"status":"gone","workspace_id":"w1","frozen":{"since":1,"why":"crash","last_at":0,"context":90000}},"w1:p2":{"status":"idle","workspace_id":"w1"}}}`)
	if err := e.Thaw("w1:p2", "revive"); err == nil || !strings.Contains(err.Error(), "not a Frozen Agent") {
		t.Errorf("thawing an Agent not Frozen: %v", err)
	}
	if err := e.Thaw("w1:p1", "melt"); err == nil {
		t.Error("an unknown choice was taken")
	}
	e.Thaw("w1:p1", "revive")
	e.Thaw("w1:p1", "fresh")
	got := e.ReadThaws()
	eq(t, "the latest choice per pane", []any{len(got), got[0].Pane, got[0].How, got[0].ID}, "[1 w1:p1 fresh w1_p1]")
	_, errs, code := s.sh("", "fresh", "w1:p2")
	eq(t, "command refuses", []any{strings.TrimSpace(errs), code}, "[berthier: w1:p2 is not a Frozen Agent 1]")
}

func TestWake(t *testing.T) {
	s := newSandbox(t)
	dir := fakeHerdr(t, s)
	e := LoadEnv(s.getenv)
	s.write("state.json", `{"agents":{"p1":{"status":"idle","name":"skipli-dark-mode","session_id":"old","workspace_label":"skipli-dark-mode"}}}`)
	s.write("reports/old.json", `{"line":"halfway through the parser","pane":"p1","at":1}`)
	if err := e.saveBrief("skipli-dark-mode", "Build dark mode."); err != nil {
		t.Fatal(err)
	}
	s.write("inbox/w1.json", `{}`)
	d := core.Delivery{ID: "w1", PaneID: "p1", From: "service", To: "skipli-dark-mode", Text: "[berthier wake] up", Wake: true, Ev: core.Fields{"pane": "p1"}}

	s.mode(dir, "takes")
	e.wake(d)
	eq(t, "clear, then rename, then prompt", calls(dir), "pane read,agent prompt,agent rename,pane read,agent prompt")
	eq(t, "self-sufficient prompt", readFile(filepath.Join(dir, "text")), "[berthier wake] up"+core.Rebuild("halfway through the parser", "Build dark mode."))
	_, err := os.Stat(filepath.Join(e.Inbox, "w1.json"))
	eq(t, "delivered and dequeued", []any{os.IsNotExist(err), strings.Contains(s.read("loop.log"), `"kind":"delivered"`)}, "[true true]")

	s.write("inbox/w1.json", `{}`)
	s.mode(dir, "noclear")
	e.wake(d)
	_, err = os.Stat(filepath.Join(e.Inbox, "w1.json"))
	eq(t, "unconfirmed clear: nothing more sent, retried", []any{strings.Contains(calls(dir), "agent rename"), readFile(filepath.Join(dir, "text")), err == nil, strings.Contains(s.read("loop.log"), `"kind":"wake_clear_failed"`)}, "[false /clear true true]")

	s.mode(dir, "typing")
	e.wake(d)
	eq(t, "a draft blocks the clear", strings.Contains(calls(dir), "agent prompt"), false)
}
