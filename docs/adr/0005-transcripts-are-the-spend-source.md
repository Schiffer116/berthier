# Claude transcripts are the source of Spend; the status-line ledger only gives quota

Budgets used to attribute the account's weekly quota from `berthier sample`: Claude Code's status line reports each session's `cost.total_cost_usd` and the account's `week_pct`, and each rise in `week_pct` was split across the sessions whose cost rose since the last rise. That answers "which Corps used the week" coarsely and nothing more: the status line only renders while a session is on screen, its cost is one opaque number per session (no model, no token kind, no turn), and subagents, cache misses or a bloated context are invisible in it. The user wants to "token max": see where Spend goes per Corps, Division, session and turn, and let optimizing Agents act on it.

Claude Code already writes everything needed: each session's transcript (`~/.claude/projects/<project>/<session>.jsonl`, subagents under `<session>/subagents/agent-<id>.jsonl`) carries, on every assistant message, the API `usage`: input, cache read, cache write split by TTL (`cache_creation.ephemeral_5m/1h_input_tokens`), output, and the model, plus the `cwd`, git branch, the prompts that started each turn and every tool call and result.

So:

- **Spend comes from transcripts.** Raw tokens by kind and model are the ground truth; API-equivalent dollars (`core.Prices`, a per-model table taken from Anthropic's published prices, never from memory) are the common currency; quota % is derived. A message's `usage` repeats on every content block of that message: it is counted once per `message.id`.
- **The Profile is an incremental index** in `state/profile/`: `Env.UpdateProfile`, run by every tick (and by `profile` and `budget`), reads only the transcript bytes past each file's cursor and folds them (`core.FoldTranscript`, pure) into rows of `turns.jsonl`, one per turn and model and pass (rows with the same key merge on read). It keeps the current and previous four quota weeks of turns; older weeks are compacted into per-session totals (`archive.json`).
- **The ledger stays for the account's quota only:** `week_pct`, `resets_at` and the five-hour window, which transcripts do not have. Each rise in `week_pct` is now split across the Spend of turns since the previous rise, by Corps (`core.Usage.Fold`), so budgets keep their meaning (% of the week) with a better attribution. A rise no Spend explains (claude.ai, another machine) goes to `other`.
- **Command overhead is its own line:** Berthier's sessions (cwd in Berthier's repo, not a Division) count as `berthier`, not as the Corps whose workspace Berthier lives in.
- **Turn origin** is read from the prompt that started a turn: every prompt Berthier sends carries a tag (`[berthier-loop]`, `[berthier ask]`, `[berthier notify]`, `[berthier brief]`, `[berthier continue]`, `[berthier resume]`); untagged is the user.

Consequences:

- The loop reads transcripts it does not own, read-only. The first fold after install reads a few weeks of transcripts at once (seconds); later ticks read only what was appended.
- `berthier profile` is the one view model (JSON) for the user, the Console and optimizing Agents; `--waste` ranks Waste signals with evidence. Shaping stays in `core`.
- Prices change: a model missing from `core.Prices` is counted in tokens but priced at zero and flagged `unpriced`, so a new model shows up rather than silently costing nothing.
- `usage.json` records its `source`; one without `transcripts` is refolded from the ledger (and `ledger.jsonl.prev`) once, so the current week is reattributed on upgrade.
