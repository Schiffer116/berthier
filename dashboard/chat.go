package main

// The chat view of one Agent: its Claude Code session transcript, read-only,
// or the pane's recent terminal output for any other kind of Agent.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ChatItem is one entry of the conversation as the page shows it.
type ChatItem struct {
	Role   string `json:"role"` // user | assistant | tool
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Input  string `json:"input,omitempty"`
	Result string `json:"result,omitempty"`
	Error  bool   `json:"error,omitempty"`
	At     string `json:"at,omitempty"`
}

const (
	chatItems   = 150  // most recent items sent to the page
	inputMax    = 300  // characters of a tool call's input summary
	resultMax   = 4000 // characters of a tool result
	procMaxHops = 64
)

// Transcripts parses Claude Code session transcripts incrementally: each poll
// reads only the lines appended since the last one.
type Transcripts struct {
	dir   string // ~/.claude
	mu    sync.Mutex
	files map[string]*transcript
}

type transcript struct {
	offset int64
	items  []ChatItem
	tools  map[string]int // tool_use id -> index in items
	info   AgentInfo
}

// AgentInfo is what the transcript says about the Agent itself: from the
// latest assistant message, updated as lines are appended (never re-read).
type AgentInfo struct {
	Model         string `json:"model,omitempty"`    // friendly: "Opus 5.5"
	ModelID       string `json:"model_id,omitempty"` // as the API names it
	Effort        string `json:"effort,omitempty"`
	ContextTokens int    `json:"context_tokens,omitempty"`
	ContextPct    int    `json:"context_pct,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
}

const (
	contextWindow   = 200_000
	contextWindowXL = 1_000_000 // an Agent past 200k tokens is on the 1M window
)

// friendlyModel turns "claude-opus-5-5" into "Opus 5.5" (and a dated
// "claude-haiku-4-5-20251001" into "Haiku 4.5"); unknown ids pass through.
func friendlyModel(id string) string {
	if !strings.HasPrefix(id, "claude-") {
		return id
	}
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	var ver []string
	for _, p := range parts[1:] {
		if _, err := strconv.Atoi(p); err != nil || len(p) > 2 {
			break
		}
		ver = append(ver, p)
	}
	if len(ver) == 0 {
		return id
	}
	return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + strings.Join(ver, ".")
}

// Info returns what the transcript at path says about its Agent; call it
// after Read, which keeps it current.
func (t *Transcripts) Info(path string) AgentInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr := t.files[path]; tr != nil {
		return tr.info
	}
	return AgentInfo{}
}

func NewTranscripts(claudeDir string) *Transcripts {
	return &Transcripts{dir: claudeDir, files: map[string]*transcript{}}
}

// Path finds the transcript a pane shows for session id. A pane's session can
// hand its conversation to a backgrounded ("bg") fork running in a descendant
// process, so the most recently updated such fork wins (lib/common.sh's
// claude_forked_from files the fork's Reports the same way).
func (t *Transcripts) Path(session string) string {
	type live struct {
		Pid       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		UpdatedAt int64  `json:"updatedAt"`
		Kind      string `json:"kind"`
	}
	var all []live
	files, _ := filepath.Glob(filepath.Join(t.dir, "sessions", "*.json"))
	for _, f := range files {
		var s live
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &s) == nil && s.SessionID != "" {
			all = append(all, s)
		}
	}
	best, bestAt := session, int64(-1)
	for _, root := range all {
		if root.SessionID != session {
			continue
		}
		for _, s := range all {
			if s.Kind == "bg" && s.SessionID != session && s.UpdatedAt > bestAt && descends(s.Pid, root.Pid) {
				best, bestAt = s.SessionID, s.UpdatedAt
			}
		}
	}
	for _, id := range []string{best, session} {
		if m, _ := filepath.Glob(filepath.Join(t.dir, "projects", "*", id+".jsonl")); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// descends reports whether pid runs under ancestor, following /proc ppids.
func descends(pid, ancestor int) bool {
	for i := 0; i < procMaxHops && pid > 1; i++ {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		// Fields after the parenthesised command: state ppid ...
		f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
		if len(f) < 2 {
			return false
		}
		if pid, _ = strconv.Atoi(f[1]); pid == ancestor {
			return true
		}
	}
	return false
}

// Read returns the latest items of the transcript at path and a revision that
// changes whenever they do.
func (t *Transcripts) Read(path string) ([]ChatItem, int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr := t.files[path]
	if st, err := os.Stat(path); err != nil {
		return nil, 0, err
	} else if tr == nil || st.Size() < tr.offset {
		tr = &transcript{tools: map[string]int{}}
		t.files[path] = tr
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	if _, err := f.Seek(tr.offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil { // a partial last line waits for the next read
			break
		}
		tr.offset += int64(len(line))
		tr.add(line)
	}
	items := tr.items
	if len(items) > chatItems {
		items = items[len(items)-chatItems:]
	}
	return items, tr.offset, nil
}

type entry struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	GitBranch   string `json:"gitBranch"`
	Cwd         string `json:"cwd"`
	Effort      string `json:"effort"`
	Message     struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Attachment struct {
		Type        string `json:"type"`
		Prompt      string `json:"prompt"`
		CommandMode string `json:"commandMode"`
		Timestamp   string `json:"timestamp"`
	} `json:"attachment"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (tr *transcript) add(line []byte) {
	var e entry
	if json.Unmarshal(line, &e) != nil || e.IsMeta || e.IsSidechain {
		return
	}
	if e.GitBranch != "" {
		tr.info.Branch = e.GitBranch
	}
	if e.Cwd != "" {
		tr.info.Cwd = e.Cwd
	}
	if m := e.Message.Model; e.Type == "assistant" && m != "" && m != "<synthetic>" {
		u := e.Message.Usage
		n := u.Input + u.CacheRead + u.CacheCreation
		win := contextWindow
		if n > win {
			win = contextWindowXL
		}
		tr.info.ModelID, tr.info.Model, tr.info.Effort = m, friendlyModel(m), e.Effort
		tr.info.ContextTokens, tr.info.ContextPct = n, (n*100+win/2)/win
	}
	switch e.Type {
	case "attachment": // a prompt typed while the Agent was busy
		if e.Attachment.Type == "queued_command" && e.Attachment.CommandMode == "prompt" {
			tr.items = append(tr.items, ChatItem{Role: "user", Text: e.Attachment.Prompt, At: e.Attachment.Timestamp})
		}
	case "user":
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			if t := userText(s); t != "" {
				tr.items = append(tr.items, ChatItem{Role: "user", Text: t, At: e.Timestamp})
			}
			return
		}
		var blocks []block
		json.Unmarshal(e.Message.Content, &blocks)
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := userText(b.Text); t != "" {
					tr.items = append(tr.items, ChatItem{Role: "user", Text: t, At: e.Timestamp})
				}
			case "tool_result":
				if i, ok := tr.tools[b.ToolUseID]; ok {
					tr.items[i].Result = clip(resultText(b.Content), resultMax)
					tr.items[i].Error = b.IsError
				}
			}
		}
	case "assistant":
		var blocks []block
		json.Unmarshal(e.Message.Content, &blocks)
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					tr.items = append(tr.items, ChatItem{Role: "assistant", Text: b.Text, At: e.Timestamp})
				}
			case "tool_use":
				tr.tools[b.ID] = len(tr.items)
				tr.items = append(tr.items, ChatItem{Role: "tool", Tool: b.Name, Input: clip(toolInput(b.Input), inputMax), At: e.Timestamp})
			}
		}
	}
}

// userText drops harness wrappers; a slash command shows as its name.
func userText(s string) string {
	s = strings.TrimSpace(s)
	if name, ok := between(s, "<command-name>", "</command-name>"); ok {
		args, _ := between(s, "<command-args>", "</command-args>")
		return strings.TrimSpace(name + " " + args)
	}
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func between(s, open, close string) (string, bool) {
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	s = s[i+len(open):]
	if j := strings.Index(s, close); j >= 0 {
		return s[:j], true
	}
	return "", false
}

// toolInput summarises a tool call by its most telling argument.
func toolInput(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	for _, k := range []string{"command", "file_path", "pattern", "url", "query", "description", "prompt", "skill", "action"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return string(raw)
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else if b.Type != "" {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
