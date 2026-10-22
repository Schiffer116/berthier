package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"berthier/internal/core"
)

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// statusLine is the part of Claude Code's status line JSON that sample reads.
type statusLine struct {
	SessionID *string `json:"session_id"`
	Cwd       *string `json:"cwd"`
	Workspace *struct {
		CurrentDir *string `json:"current_dir"`
	} `json:"workspace"`
	Cost *struct {
		Total *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	Limits *struct {
		SevenDay *window `json:"seven_day"`
		FiveHour *window `json:"five_hour"`
	} `json:"rate_limits"`
}

type window struct {
	Pct      *float64 `json:"used_percentage"`
	ResetsAt *float64 `json:"resets_at"`
}

// sample is run by Claude Code's status line on every render, with the
// session JSON on stdin: append {ts, session, cwd, cost, week_pct, resets_at[,
// five_pct, five_resets_at]} to the ledger (state/ledger.jsonl) when the
// session's cost or the week's usage moved since its last sample (the
// five-hour window rides along on those rows, never makes one). Silent and
// never fails: it must not disturb the status line. One short O_APPEND write
// per row, so concurrent sessions never interleave. core.Usage.Fold turns the
// ledger into per-Corps usage.
func sample(getenv func(string) string, in io.Reader) {
	var s statusLine
	if json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&s) != nil {
		return
	}
	if s.SessionID == nil || !sessionID.MatchString(*s.SessionID) || s.Limits == nil || s.Limits.SevenDay == nil || s.Limits.SevenDay.Pct == nil {
		return
	}
	home := getenv("BERTHIER_HOME")
	if home == "" {
		root := getenv("BERTHIER_ROOT")
		if root == "" {
			root = guessRoot()
		}
		home = filepath.Join(root, "state")
	}
	row := core.Row{Ts: float64(time.Now().Unix()), Session: *s.SessionID, Cwd: s.Cwd, WeekPct: *s.Limits.SevenDay.Pct, ResetsAt: s.Limits.SevenDay.ResetsAt}
	if row.Cwd == nil && s.Workspace != nil {
		row.Cwd = s.Workspace.CurrentDir
	}
	if s.Cost != nil && s.Cost.Total != nil {
		row.Cost = *s.Cost.Total
	}
	if f := s.Limits.FiveHour; f != nil && f.Pct != nil {
		row.FivePct, row.FiveResetsAt = f.Pct, f.ResetsAt
	}
	var key bytes.Buffer
	enc := json.NewEncoder(&key)
	enc.SetEscapeHTML(false)
	enc.Encode([]any{row.Cost, row.WeekPct, row.ResetsAt, row.Cwd})
	k := bytes.TrimSpace(key.Bytes())
	dir := filepath.Join(home, "sampled")
	if last, err := os.ReadFile(filepath.Join(dir, row.Session)); err == nil && bytes.Equal(last, k) {
		return
	}
	os.MkdirAll(dir, 0o755)
	if os.WriteFile(filepath.Join(dir, row.Session), k, 0o644) != nil {
		return
	}
	line, err := json.Marshal(row)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, "ledger.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}
