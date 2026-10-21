# Developing Berthier

`CLAUDE.md` is Berthier's identity, not developer docs: the repo is Berthier's home, and any agent launched here is Berthier. Vocabulary is in `CONTEXT.md`; decisions in `docs/adr/`.

## Commands

- Unit tests (pure, instant): `tests/tick.test.sh`, `tests/pane.test.sh`, `tests/counts.test.sh`, `tests/commands.test.sh`, `tests/prompt.test.sh` (`tests/fake-herdr` stands in for Herdr, also in the Go tests)
- E2E (real Herdr + Claude haiku, isolated `berthier-e2e-<pid>` session, so runs can overlap, ~1 min): `tests/e2e.sh`
- Lint: `shellcheck bin/berthier libexec/* lib/common.sh tests/*.sh` (`.shellcheckrc` resolves sources)
- Try against a scratch Herdr session: `BERTHIER_HERDR_SESSION=<name> BERTHIER_HOME=<tmpdir> bin/berthier <cmd>`
- Loop service: `systemctl --user {status,restart} berthier-loop`; events in `state/loop.log`, read with `berthier log [-f]`.
- Console: `systemctl --user {status,restart} berthier-dashboard`, http://127.0.0.1:8430; Go tests: `cd dashboard && go test ./...`.

## Architecture

- `bin/berthier` (linked into `~/.local/bin`) dispatches to `libexec/<cmd>`; every script sources `lib/common.sh` (paths, config env vars, the `hd` Herdr wrapper).
- Main Loop = `libexec/loop` -> `libexec/tick` every 10s, run by `systemd/berthier-loop.service`. A tick: `herdr api snapshot` + previous `state/state.json` -> `lib/tick.jq` (pure decisions: Revives, escalations, Corps grouping, and the whole next Army State) -> bash carries out its actions -> atomic state write. A decision that needs a pane read (deliberate exit, shell prompt, usage-limit stop) is listed under `look`; bash reads just those panes with the classifiers in `lib/common.sh` and reruns `tick.jq` with `$obs`, so a tick is one run, or two. Only `tick.jq` writes the Army State: bash never patches it, so every decision stays unit-testable.
- Events: `state/loop.log` is JSON Lines, `{ts, kind, msg, ...fields}` (`pane`, `corps`, `to`, `why`, ...), one per thing the loop decided (`tick.jq`'s `events`) or did (bash's `event` helper, from each action's `ev` fields). `msg` is for people; code and tests match on `kind` and fields. `army.jq` gives each event its Console tag and label and skips non-JSON lines; the loop moves a pre-JSON log to `loop.log.pre-json` once.
- Console = `libexec/dashboard` (rebuilds `dashboard/.bin` when sources change) running `dashboard/main.go`, which polls `berthier army` (`lib/army.jq`, the pure view model) every 2s and pushes changes over SSE to `dashboard/index.html` (vanilla JS; its one write is `POST /api/prompt`, `PromptAgent` to a pane in the view model; `GET /api/chat` reads that Agent's Claude Code transcript, following forked sessions via `~/.claude/sessions`, else its terminal). Shaping data belongs in `army.jq`, not the page. It installs as a phone app (`app.go`: manifest, `sw.js` app-shell worker that never touches `api/*`, icons drawn in Go).
- Prompting an Agent: always `prompt_agent` (`lib/common.sh`) / `PromptAgent` (`dashboard/prompt.go`), never bare `herdr agent prompt`, which only types text + Enter: Claude drops that Enter while it resizes pasted image paths, leaving a draft. They confirm Claude took it (state change, or a busy Agent's box emptied) and retry Enter or the paste. Herdr also refuses multi-line `agent start` arguments, so `start_claude` sends the Brief this way.
- Spoken alerts: when the user is needed (blocked `BERTHIER_SPEAK_AFTER`s, Revive given up), `tick` runs the dotfiles' `notify-user` `speak.sh` itself, once per episode, without waiting on Berthier.
- Coordination: `ask` drops a JSON file in `state/inbox/`; `tick` feeds the inbox to `tick.jq`, which picks deliveries (addressee idle, one per Agent per tick) and escalates asks stuck past `BERTHIER_ASK_STUCK` or flipping direction between two Agents `BERTHIER_PINGPONG_MAX`+ times an hour (one-way updates don't count). Addressing names (`corps_name`) live in `lib/names.jq`, shared with the commands through `jqn`. Ownership is `owners.md`.
- Reports: `report` writes `state/reports/<claude session id>.json`; `counts` joins them with live Agents for the bar. `hook-stop` is a Claude Code Stop hook (wired globally by the dotfiles) that blocks a turn's end in a Herdr pane until the Agent has reported during it.
- Berthier = the agent whose cwd is this repo; the loop prompts escalations into its pane, and sends Berthier's own problems to `herdr notification show`.
- Revive = `herdr agent start <name> --kind claude --pane <pane> -- --resume <session>`, only when the pane survived at a shell prompt. Deliberate stops are forgotten, never Revived: a closed pane, or a clean Claude exit (it prints `Resume this session with: claude --resume <id>`; a killed process does not), read on the tick the agent vanishes and again before any Revive. A pane still holding the session with no agent listed is Herdr resuming it after a restart, not a crash: never Revived.
- Usage limits: Claude rarely continues by itself after its usage limit resets. `tick.jq` asks for each idle episode (Herdr's `state_change_seq` + `status_since`, so a turn between two ticks counts) to be read once; `usage_limit_reset` (`lib/common.sh`) finds a limit stop as the last output above the input box and parses its reset time and zone; after the reset (+`BERTHIER_LIMIT_GRACE`) `tick` prompts `BERTHIER_LIMIT_PROMPT` once per episode, never twice within `BERTHIER_LIMIT_RETRY`.
- Names: Herdr forgets agent names across a restart unless it assigned them at `agent start`, and drops the name when `agent start` times out. `tick` renames an unnamed agent back to the name its session had in `state.json`.
- Divisions are the workspaces `state/divisions.json` (`division_set`) records, as `{corps, worktree}`; that record, not the label, makes one a Division (`is_division`). `division-new`/`division-done` create and remove worktree Divisions (`herdr worktree create`, default `~/.herdr/worktrees/`); `adopt` records an existing workspace. `tick.jq` nests Divisions under their Corps and routes their escalations (`to`) to the Corps's Main Agent; only a spawned Division's stop escalates.
- Runtime data is gitignored: `state/` (loop) and `notes.md` (Berthier's Notes for session continuity, imported by `CLAUDE.md`; Berthier edits the "How the user works" section of `CLAUDE.md` itself). Berthier's skills go in `.claude/skills/`.

## Gotchas

- Never experiment on the user's default Herdr session; use a named session started headlessly: `tmux new -d "env -u HERDR_ENV -u HERDR_SOCKET_PATH herdr session attach <name>"`.
- Herdr fills workspace `worktree` metadata lazily; ask git (`rev-parse --git-common-dir`) instead of trusting it.
- A Claude session with no messages is not saved and cannot be resumed.
- New directories trigger Claude's folder trust dialog (Agent shows `blocked`); trusting a parent dir covers children.
- `CLAUDE.md` `@` imports of files outside the session's cwd need interactive approval and silently drop in `claude -p`.
