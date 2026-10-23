// Package app is Berthier's I/O shell: configuration from the environment,
// Herdr, the files of state/, and the commands. It decides nothing itself;
// internal/core does (ADR 0001, 0004).
package app

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"berthier/internal/core"
	"berthier/internal/herdr"
)

// Env is the paths and tunables every command shares.
type Env struct {
	Root, Home                                   string
	State, Log, Inbox, Reports, Demos, Divisions string
	Ledger, Usage, Budgets, BoardsFile           string
	SkipliBin, SkipliEnv                         string
	BoardPoll                                    int64
	H                                            herdr.Client
	HerdrSession                                 string
	Interval                                     string // raw, as the loop prints it
	IntervalSecs                                 float64
	BlockedAfter, LongAfter, DivisionSettle      int64
	ReviveMax                                    int
	ReviveBackoff, ReviveReset, EscalationTTL    int64
	SpeakAfter, AskStuck                         int64
	PingpongMax                                  int
	LimitGrace, LimitRetry                       int64
	LimitPrompt                                  string
	RestartGap, RestartWait, RestartSettle       int64
	RestartPrompt                                string
	LedgerMax                                    int64
	ProfileWeeks                                 int
	ProfileSince                                 float64
	ProfileProjects                              string
	BudgetWarn                                   float64
	Speak                                        string
	DivisionModel                                string
	StartTimeoutMS                               int64
	PromptPoll                                   time.Duration
	ClaudeDir                                    string
	Systemctl, ConsoleURL                        string
	LandWait                                     time.Duration
	Now                                          func() time.Time
	getenv                                       func(string) string
}

// LoadEnv reads the BERTHIER_* variables (see CONTRIBUTING.md) from getenv.
// A malformed number falls back to its default. It creates state/ and its
// inbox and reports directories.
func LoadEnv(getenv func(string) string) Env {
	str := func(k, d string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return d
	}
	num := func(k string, d float64) float64 {
		if f, err := strconv.ParseFloat(getenv(k), 64); err == nil {
			return f
		}
		return d
	}
	i64 := func(k string, d int64) int64 { return int64(num(k, float64(d))) }
	home, _ := os.UserHomeDir()
	e := Env{getenv: getenv, Now: time.Now}
	e.Root = getenv("BERTHIER_ROOT")
	if e.Root == "" {
		e.Root = guessRoot()
	}
	// Runtime data lives in the repo (gitignored), next to the Berthier session.
	e.Home = str("BERTHIER_HOME", filepath.Join(e.Root, "state"))
	e.State = filepath.Join(e.Home, "state.json")
	e.Log = filepath.Join(e.Home, "loop.log")
	e.Inbox = filepath.Join(e.Home, "inbox")
	e.Reports = filepath.Join(e.Home, "reports")
	e.Demos = filepath.Join(e.Home, "demos")
	e.Divisions = filepath.Join(e.Home, "divisions.json")
	e.Ledger = filepath.Join(e.Home, "ledger.jsonl")
	e.Usage = filepath.Join(e.Home, "usage.json")
	e.Budgets = filepath.Join(e.Home, "budgets.json")
	e.BoardsFile = filepath.Join(e.Home, "boards.json")
	// Empty means the user's default Herdr session; tests point this at an
	// isolated named session so they never touch live workspaces.
	e.HerdrSession = getenv("BERTHIER_HERDR_SESSION")
	e.Interval = str("BERTHIER_INTERVAL", "10")
	e.IntervalSecs = num("BERTHIER_INTERVAL", 10)
	e.BlockedAfter = i64("BERTHIER_BLOCKED_AFTER", 120)
	e.LongAfter = i64("BERTHIER_LONG_AFTER", 3600)
	e.DivisionSettle = i64("BERTHIER_DIVISION_SETTLE", 30)
	e.ReviveMax = int(i64("BERTHIER_REVIVE_MAX", 3))
	e.ReviveBackoff = i64("BERTHIER_REVIVE_BACKOFF", 60)
	e.ReviveReset = i64("BERTHIER_REVIVE_RESET", 300)
	e.EscalationTTL = i64("BERTHIER_ESCALATION_TTL", 604800)
	e.SpeakAfter = i64("BERTHIER_SPEAK_AFTER", 30)
	// Desktop notification + chime + TTS, from the dotfiles' notify-user skill.
	e.Speak = str("BERTHIER_SPEAK", filepath.Join(home, ".claude/skills/notify-user/speak.sh"))
	e.AskStuck = i64("BERTHIER_ASK_STUCK", 1800)
	e.PingpongMax = int(i64("BERTHIER_PINGPONG_MAX", 10))
	// Usage limits: nudge a stopped Agent this long after its limit resets,
	// and never twice within RETRY (a nudge that hits the limit again starts over).
	e.LimitGrace = i64("BERTHIER_LIMIT_GRACE", 60)
	e.LimitRetry = i64("BERTHIER_LIMIT_RETRY", 1800)
	e.LimitPrompt = str("BERTHIER_LIMIT_PROMPT", "The usage limit has reset. Continue exactly where you left off.")
	// Restart: a gap this long since the last state write (loop down, Herdr
	// unreachable) may have cut Agents off mid-turn; wait this long for each to
	// reappear, then prompt it once.
	e.RestartGap = i64("BERTHIER_RESTART_GAP", 45)
	e.RestartWait = i64("BERTHIER_RESTART_WAIT", 3600)
	e.RestartSettle = i64("BERTHIER_RESTART_SETTLE", 20)
	e.RestartPrompt = str("BERTHIER_RESTART_PROMPT", "The machine restarted and interrupted your turn; continue where you left off.")
	e.LedgerMax = i64("BERTHIER_LEDGER_MAX", 4194304)
	// The Profile keeps this many quota weeks of turns (the current one
	// included), and never reads transcripts from before PROFILE_SINCE (epoch
	// seconds; tests set it to their start).
	e.ProfileWeeks = int(max(i64("BERTHIER_PROFILE_WEEKS", 5), 1))
	e.ProfileSince = num("BERTHIER_PROFILE_SINCE", 0)
	// Which of Claude's project directories (a glob) the Profile reads.
	e.ProfileProjects = str("BERTHIER_PROFILE_PROJECTS", "*")
	// Share of its budget at which a Corps's Main Agent is told and the user hears (percent).
	e.BudgetWarn = num("BERTHIER_BUDGET_WARN", 80)
	// Boards: skipli-cli, on one Skipli Environment (dev for now), polled this often (seconds).
	e.SkipliBin = str("BERTHIER_SKIPLI_BIN", "skipli-cli")
	e.SkipliEnv = str("BERTHIER_SKIPLI_ENV", "dev")
	e.BoardPoll = i64("BERTHIER_BOARD_POLL", 60)
	// Claude model of spawned Divisions (division-new --model overrides); Main
	// Agents and Berthier use claude's own default.
	e.DivisionModel = str("BERTHIER_DIVISION_MODEL", "sonnet")
	e.StartTimeoutMS = i64("BERTHIER_START_TIMEOUT_MS", 45000)
	// How often prompt_agent checks that Claude took a prompt (seconds).
	e.PromptPoll = time.Duration(num("BERTHIER_PROMPT_POLL", 0.5) * float64(time.Second))
	// Claude Code's own directory: live sessions (sessions/<pid>.json) and transcripts.
	e.H = herdr.FromEnv(getenv, e.PromptPoll)
	e.ClaudeDir = str("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	// `land` restarts services with this and waits up to LandWait for them to work.
	e.Systemctl = str("BERTHIER_SYSTEMCTL", "systemctl")
	e.ConsoleURL = str("BERTHIER_CONSOLE_URL", "http://127.0.0.1:8430/")
	e.LandWait = time.Duration(num("BERTHIER_LAND_WAIT", 120) * float64(time.Second))
	for _, d := range []string{e.Home, e.Inbox, e.Reports, e.Demos} {
		os.MkdirAll(d, 0o755)
	}
	return e
}

// guessRoot is the repository the running binary lives in (<root>/bin/berthier).
func guessRoot() string {
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			return filepath.Dir(filepath.Dir(exe))
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func (e *Env) now() int64 { return e.Now().Unix() }

// Config is the Main Loop's tunables for core.Tick.
func (e *Env) CoreConfig() (c core.Config) {
	return core.Config{BerthierRoot: e.Root, BlockedAfter: e.BlockedAfter, LongAfter: e.LongAfter, DivisionSettle: e.DivisionSettle,
		ReviveMax: e.ReviveMax, ReviveBackoff: e.ReviveBackoff, ReviveReset: e.ReviveReset, EscalationTTL: e.EscalationTTL,
		SpeakAfter: e.SpeakAfter, AskStuck: e.AskStuck, PingpongMax: e.PingpongMax, LimitGrace: e.LimitGrace,
		LimitRetry: e.LimitRetry, BudgetWarn: e.BudgetWarn,
		RestartGap: e.RestartGap, RestartWait: e.RestartWait, RestartSettle: e.RestartSettle}
}
