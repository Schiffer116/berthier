package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func writeJSONL(t *testing.T, path string, lines ...string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		f.WriteString(l)
	}
}

func TestTranscriptReadsTheConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, path,
		`{"type":"user","timestamp":"t1","message":{"content":"fix the bug"}}`+"\n",
		`{"type":"user","isMeta":true,"message":{"content":"meta"}}`+"\n",
		`{"type":"user","message":{"content":"<local-command-stdout>x</local-command-stdout>"}}`+"\n",
		`{"type":"user","message":{"content":"<command-name>/review</command-name><command-args>42</command-args>"}}`+"\n",
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent"}]}}`+"\n",
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"Looking."},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls","description":"List"}}]}}`+"\n",
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":[{"type":"text","text":"a.go"}],"is_error":true}]}}`+"\n",
		`{"type":"attachment","attachment":{"type":"queued_command","prompt":"also this","commandMode":"prompt","timestamp":"t2"}}`+"\n",
		`{"type":"attachment","attachment":{"type":"queued_command","prompt":"<task-notification>","commandMode":"task-notification"}}`+"\n",
		`{"type":"assistant","message":{"content":[{"type":"text","text":"half`, // still being written
	)
	tr := NewTranscripts(t.TempDir())
	items, rev, err := tr.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []ChatItem{
		{Role: "user", Text: "fix the bug", At: "t1"},
		{Role: "user", Text: "/review 42"},
		{Role: "assistant", Text: "Looking."},
		{Role: "tool", Tool: "Bash", Input: "ls", Result: "a.go", Error: true},
		{Role: "user", Text: "also this", At: "t2"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("items\n got %+v\nwant %+v", items, want)
	}

	writeJSONL(t, path, ` done"}]}}`+"\n")
	items, rev2, _ := tr.Read(path)
	if rev2 <= rev || len(items) != 6 || items[5].Text != "half done" {
		t.Fatalf("after append: rev %d -> %d, items %+v", rev, rev2, items)
	}
}

func TestTranscriptPathFollowsTheLiveDescendantSession(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
	os.MkdirAll(filepath.Join(dir, "projects", "-some-cwd"), 0o755)
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	session := func(pid int, id string, at int64) {
		b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "updatedAt": at})
		os.WriteFile(filepath.Join(dir, "sessions", fmt.Sprint(pid)+".json"), b, 0o644)
		os.WriteFile(filepath.Join(dir, "projects", "-some-cwd", id+".jsonl"), nil, 0o644)
	}
	tr := NewTranscripts(dir)
	session(os.Getpid(), "pane", 1)
	session(1, "unrelated", 99)
	if got := tr.Path("pane"); filepath.Base(got) != "pane.jsonl" {
		t.Fatalf("no descendant: %q", got)
	}
	session(child.Process.Pid, "fork", 2)
	if got := tr.Path("pane"); filepath.Base(got) != "fork.jsonl" {
		t.Fatalf("with a forked session: %q", got)
	}
	if got := tr.Path("missing"); got != "" {
		t.Fatalf("unknown session: %q", got)
	}
}

// fakeChatHerdr answers `agent get` with the given agent kind and session and
// `agent read` with fixed terminal text.
func fakeChatHerdr(t *testing.T, kind, session string) string {
	script := filepath.Join(t.TempDir(), "herdr")
	os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
case "$2" in
get) echo '{"result":{"agent":{"agent":"%s","agent_status":"working","agent_session":{"value":"%s"}}}}' ;;
read) printf 'terminal text\n' ;;
esac
`, kind, session)), 0o755)
	return script
}

func getChat(t *testing.T, url string) map[string]any {
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET %s = %d", url, res.StatusCode)
	}
	var v map[string]any
	json.NewDecoder(res.Body).Decode(&v)
	return v
}

func TestChatShowsTranscriptOrTerminal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "projects", "-cwd"), 0o755)
	writeJSONL(t, filepath.Join(dir, "projects", "-cwd", "s1.jsonl"), `{"type":"user","message":{"content":"hi"}}`+"\n")
	a := NewArmy()
	a.Set([]byte(promptArmy))

	srv := httptest.NewServer(Handler(a, fakeChatHerdr(t, "claude", "s1"), NewTranscripts(dir)))
	defer srv.Close()
	v := getChat(t, srv.URL+"/api/chat?pane=wA:p1")
	if v["source"] != "transcript" || v["session"] != "s1" || v["status"] != "working" || len(v["items"].([]any)) != 1 {
		t.Fatalf("claude agent: %v", v)
	}
	if res, _ := http.Get(srv.URL + "/api/chat?pane=wZ:p9"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown pane = %d", res.StatusCode)
	}

	srv2 := httptest.NewServer(Handler(a, fakeChatHerdr(t, "codex", ""), NewTranscripts(dir)))
	defer srv2.Close()
	if v := getChat(t, srv2.URL+"/api/chat?pane=wA:p1"); v["source"] != "terminal" || v["text"] != "terminal text\n" {
		t.Fatalf("other agent: %v", v)
	}
}
