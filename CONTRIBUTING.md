# Developing Berthier

`CLAUDE.md` is Berthier's identity, not developer docs: the repo is Berthier's home, and any agent launched here is Berthier. Vocabulary is in `CONTEXT.md`; decisions in `docs/adr/`.

## Commands

- Unit tests (pure, instant): `tests/tick.test.sh`, `tests/counts.test.sh`, `tests/commands.test.sh`
- E2E (real Herdr + Claude haiku, isolated `berthier-e2e` session, ~1 min): `tests/e2e.sh`
- Lint: `shellcheck bin/berthier libexec/* lib/common.sh tests/*.sh` (`.shellcheckrc` resolves sources)
- Try against a scratch Herdr session: `BERTHIER_HERDR_SESSION=<name> BERTHIER_HOME=<tmpdir> bin/berthier <cmd>`
- Loop service: `systemctl --user {status,restart} berthier-loop`; log in `state/loop.log`.

## Architecture

- `bin/berthier` (linked into `~/.local/bin`) dispatches to `libexec/<cmd>`; every script sources `lib/common.sh` (paths, config env vars, the `hd` Herdr wrapper).
- Main Loop = `libexec/loop` -> `libexec/tick` every 10s, run by `systemd/berthier-loop.service`. A tick: `herdr api snapshot` + previous `state/state.json` -> `lib/tick.jq` (pure decisions: Revives, escalations, Corps grouping) -> bash executes them -> atomic state write. Keep decisions in `tick.jq` so they stay unit-testable; `tick` is the only writer of `state.json`.
- Spoken alerts: when the user is needed (blocked `BERTHIER_SPEAK_AFTER`s, Revive given up), `tick` runs the dotfiles' `notify-user` `speak.sh` itself, once per episode, without waiting on Berthier.
- Coordination: `ask` drops a JSON file in `state/inbox/`; `tick` feeds the inbox to `tick.jq`, which picks deliveries (addressee idle, one per Agent per tick) and escalates asks stuck past `BERTHIER_ASK_STUCK` or flipping direction between two Agents `BERTHIER_PINGPONG_MAX`+ times an hour (one-way updates don't count). Addressing names (`corps_name`) live in `lib/names.jq`, shared with the commands through `jqn`. Ownership is `owners.md`.
- Reports: `report` writes `state/reports/<claude session id>.json`; `counts` joins them with live Agents for the bar. `hook-stop` is a Claude Code Stop hook (wired globally by the dotfiles) that blocks a turn's end in a Herdr pane until the Agent has reported during it.
- Berthier = the agent whose cwd is this repo; the loop prompts escalations into its pane, and sends Berthier's own problems to `herdr notification show`.
- Revive = `herdr agent start <name> --kind claude --pane <pane> -- --resume <session>`, only when the pane survived at a shell prompt. Deliberate stops are forgotten, never Revived: a closed pane, or a clean Claude exit (it prints `Resume this session with: claude --resume <id>`; a killed process does not), checked on the tick the agent vanishes and again before any Revive.
- Tasks are worktree workspaces labelled `󱎦 <corps>/<task>` (`herdr worktree create`, default `~/.herdr/worktrees/`), split by Main Agents via `task-split`/`task-done`.
- Runtime data is gitignored: `state/` (loop) and `notes.md` (Berthier's Notes for session continuity, imported by `CLAUDE.md`; Berthier edits the "How the user works" section of `CLAUDE.md` itself). Berthier's skills go in `.claude/skills/`.

## Gotchas

- Never experiment on the user's default Herdr session; use a named session started headlessly: `tmux new -d "env -u HERDR_ENV -u HERDR_SOCKET_PATH herdr session attach <name>"`.
- Herdr fills workspace `worktree` metadata lazily; ask git (`rev-parse --git-common-dir`) instead of trusting it.
- A Claude session with no messages is not saved and cannot be resumed.
- New directories trigger Claude's folder trust dialog (Agent shows `blocked`); trusting a parent dir covers children.
- `CLAUDE.md` `@` imports of files outside the session's cwd need interactive approval and silently drop in `claude -p`.
