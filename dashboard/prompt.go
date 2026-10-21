package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

// PromptPoll is how often PromptAgent checks that Claude took a prompt.
var PromptPoll = 500 * time.Millisecond

// PromptAgent prompts the Agent in pane and makes sure Claude took it, like
// prompt_agent in lib/common.sh: `agent prompt` only types the text and Enter,
// and Claude swallows that Enter while it turns pasted image paths into
// attachments, leaving a draft. Taken once the Agent changes state, or, busy,
// its box is empty (queued). A draft gets Enter, a lost paste is sent again.
func PromptAgent(herdr, pane, text string) error {
	seq0, _, err := agentState(herdr, pane)
	if err != nil {
		return err
	}
	if err := runHerdr(herdr, "agent", "prompt", pane, text); err != nil {
		return err
	}
	tries := 0
	for polls := 1; ; polls++ {
		time.Sleep(PromptPoll)
		seq, status, err := agentState(herdr, pane)
		if err != nil {
			return err
		}
		if seq != seq0 {
			return nil
		}
		if polls%4 != 0 {
			continue
		}
		tail, _ := Herdr(herdr, "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "40").Output()
		holds := inputHolds(string(tail), text)
		if !holds && status == "working" {
			return nil
		}
		if tries++; tries > 3 {
			return fmt.Errorf("Claude in %s did not take the prompt", pane)
		}
		if holds {
			runHerdr(herdr, "agent", "send-keys", pane, "enter")
		} else {
			runHerdr(herdr, "agent", "prompt", pane, text)
		}
	}
}

func agentState(herdr, pane string) (seq json.Number, status string, err error) {
	out, err := Herdr(herdr, "agent", "get", pane).Output()
	var got struct {
		Result struct {
			Agent struct {
				Seq    json.Number `json:"state_change_seq"`
				Status string      `json:"agent_status"`
			}
		}
	}
	if err != nil || json.Unmarshal(out, &got) != nil || got.Result.Agent.Status == "" {
		return "", "", fmt.Errorf("no agent in %s", pane)
	}
	return got.Result.Agent.Seq, got.Result.Agent.Status, nil
}

// runHerdr runs a Herdr command; its error carries Herdr's message.
func runHerdr(herdr string, args ...string) error {
	var stderr bytes.Buffer
	cmd := Herdr(herdr, args...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err == nil || !errors.As(err, &exit) {
		return err
	}
	var e struct{ Error struct{ Message string } }
	if json.Unmarshal(stderr.Bytes(), &e) == nil && e.Error.Message != "" {
		return errors.New(e.Error.Message)
	}
	return fmt.Errorf("%v: %s", err, bytes.TrimSpace(stderr.Bytes()))
}

// inputHolds reports whether the Claude Code input box in a pane's tail still
// holds text, unsent. Claude shows pasted image paths as [Image #n] and long
// pastes as [Pasted text #n]; its idle suggestion matches neither nor the text.
func inputHolds(tail, text string) bool {
	lines := strings.Split(tail, "\n")
	var rules []int
	for i, l := range lines {
		if strings.HasPrefix(l, strings.Repeat("─", 8)) {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 {
		return false
	}
	box := strings.TrimPrefix(noSpace(strings.Join(lines[rules[len(rules)-2]+1:rules[len(rules)-1]], "")), "❯")
	if box == "" {
		return false
	}
	if strings.Contains(box, "[Image#") || strings.Contains(box, "[Pastedtext#") {
		return true
	}
	key := []rune(noSpace(text))
	n := min(len(key), 24)
	return n > 0 && (strings.Contains(box, string(key[:n])) || strings.Contains(box, string(key[len(key)-n:])))
}

func noSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
