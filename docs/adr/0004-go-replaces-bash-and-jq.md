# Berthier is one Go binary; bash and jq are gone

Berthier began as bash scripts around four jq libraries (`tick.jq`, `usage.jq`, `army.jq`, `names.jq`). The jq grew past what anyone can read: `tick.jq` alone was 365 lines of nested pipelines, `$var`s threaded through `as` bindings, and `// empty` guards, and nobody, human or agent, held enough jq in their head to change it without breaking a neighbouring decision. The bash around it had the same problem in smaller form (quoting, `set -e` subtleties, `mapfile` loops over `@tsv`).

So everything is now Go, built into one binary, `berthier`, with the same CLI, the same `state/` file formats (`state.json`, `usage.json`, `ledger.jsonl`, `divisions.json`, `budgets.json`, the inbox, the reports, `loop.log` as JSON Lines) and the same systemd units. The shape [ADR 0001](0001-deterministic-core-llm-at-the-edge.md) chose is kept, and now enforced by the compiler instead of by convention:

- `internal/core` is pure and does no I/O, reads no clock, no environment, no files: `Tick` (the whole next Grand Army State plus the actions to carry out), `Usage.Fold` and `Usage.View` (weekly quota attribution), `Army` (the view model), the names, and the classifiers over a pane's text (`UsageLimitReset`, `ExitedDeliberately`, `InputHolds`). Typed inputs and outputs replace `--argjson` blobs. They are unit-tested as table tests, which are the old `tests/*.test.sh` cases ported one to one, with the same expected values.
- `internal/herdr` and `internal/app` are the I/O shell: Herdr, the files of `state/`, locks, the commands, the Main Loop. They gather observations, call `core`, and carry out what it decided. `core.Tick` still alone writes the Grand Army State.
- `internal/console` is the Console server (it was its own Go module); `berthier dashboard` runs it in-process and reads the view model by calling `Army` instead of polling a subprocess.

`sample` runs on every status-line render, so it stays fast (a static binary, about 5 ms, against forking bash and jq several times), silent and exit 0 whatever happens.

`tests/e2e.sh` stays the black-box gate (real Herdr, real Claude haiku) and passed before and after the port. `tests/fake-herdr` remains as a fixture for the prompt tests.

Consequences:

- Go is a build dependency: `go build -o bin/berthier ./cmd/berthier`. The binary is not committed. The systemd units build it before starting, as `libexec/dashboard` used to rebuild the Console.
- `go vet` and `gofmt` replace shellcheck as the lint.
- Objects written by Go list their keys in a fixed order (struct order, or sorted for maps like `by_corps`), where jq kept insertion order. Readers match on keys, so nothing notices.
- A few jq leniencies are gone: a malformed `state/` file reads as empty rather than aborting a command half way.
