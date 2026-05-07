# lazyduckdb

A TUI for querying parquet files with DuckDB. Built with Bubble Tea — `cmd/<app>/main.go` entry, feature packages under `internal/`. In the results pane, press `/` to open a client-side incremental search: typing filters highlights live across every loaded row (case-insensitive), Enter cycles to the next match (wraps around), and Esc exits search — arrow keys keep scrolling while highlights stay on.

## Keybindings: follow macOS conventions

This is primarily a macOS app. Keybindings should feel native. Always register **both** a Cmd form and a Ctrl form for every primary action — the Cmd form is what the user reaches for, the Ctrl form is the fallback for terminals that don't forward Cmd.

| Action | Bind |
| --- | --- |
| Run query (auto-focuses results) | `super+r`, `ctrl+r` |
| Export to Excel | `super+e`, `ctrl+e` |
| Toggle editor ↔ results | `esc` (primary), `ctrl+q` (editor-only fallback) |
| Word left | `alt+left`, `alt+b` |
| Word right | `alt+right`, `alt+f` |
| Line start / end | `home`/`end` (add `ctrl+a`/`ctrl+e` if needed) |

Focus model is query-driven: running a query auto-focuses results, and `esc` is a symmetric toggle between the two panes (issue #1). The editor→results half of the toggle is gated on a result set being loaded — without one there's nothing to scroll, so `esc` is a no-op there. Don't re-introduce a manual `super+t` / `ctrl+t` focus-results binding — `cmd+t` is swallowed by every macOS terminal (New Tab), and the toggle covers the same need with one fewer key. The editor's own `esc` handler (dismiss completion list) takes priority when a completion is open.

### Why two forms for each

**Cmd as `super+*`**: On macOS the Command key is reported as `super` by the Kitty keyboard protocol. This app targets Bubble Tea v2, which requests Kitty by default, so terminals that support it (Ghostty, Kitty, WezTerm, modern iTerm2 with reporting enabled) deliver `super+r` etc. to the app. Terminals that don't support Kitty (macOS Terminal.app, older iTerm2 configs) swallow the Cmd chord and it never arrives — that's when the ctrl fallback saves the user.

**Option as both `alt+<arrow>` and `alt+<letter>`**: macOS Terminal (default) and iTerm2's "Natural text editing" preset send Option+Arrow as `ESC b`/`ESC f` (readline `backward-word`/`forward-word`), which Bubble Tea surfaces as `alt+b`/`alt+f`. Users who've set "Left Option key = Esc+" or enabled CSI-u mode get `alt+left`/`alt+right` instead. Handling only one form breaks the app for half the userbase.

### The rule

When you add a new shortcut, ask:
- Is there a Cmd-equivalent a macOS user would reach for? Bind both `super+*` and `ctrl+*`.
- Does it involve Option+Arrow? Bind both `alt+<arrow>` and the readline `alt+<letter>` alias.
- Don't use `cmd+c` / `super+c` — it collides with Copy in every terminal. `ctrl+c` stays Quit.

## Project layout

- `cmd/lazyduckdb/main.go` — entrypoint, handles the CLI arg / picker branch
- `internal/duck` — DuckDB session (opens parquet as view `t`, runs queries)
- `internal/editor` — multi-line SQL editor with tab-complete
- `internal/table` — horizontally scrollable results view
- `internal/export` — xlsx export via excelize
- `internal/picker` — parquet file selector (used when no CLI arg)
- `internal/app` — Bubble Tea root model that wires everything together
- `internal/keymap` — binding defaults, centralized so they're easy to audit

## Running

```
go run ./cmd/lazyduckdb [parquet_file]
```

With no argument it lists `*.parquet` in the current directory and lets you pick one.

## Shortcut: "install"

When the user's prompt is just the word `install` (case-insensitive, with or without trailing punctuation), run:

```
go install ./cmd/lazyduckdb
```

This drops a fresh `lazyduckdb` binary into `$(go env GOPATH)/bin` (`~/go/bin` on this machine, which is on the user's PATH). Confirm with `which lazyduckdb && lazyduckdb -v` and report the result. Don't ask for confirmation — `install` is the confirmation.

## Shortcut: "release"

When the user's prompt is `release` or `release <X.Y.Z>` (case-insensitive, optional `v` prefix), prepare a new release **locally only** — bump, commit, tag — then hand the user the exact commands to push and publish. The word itself is the confirmation; don't ask again, but DO run the steps in order and stop on the first failure.

Why local-only: the sandbox blocks `git push github master` (pushing to the default branch bypasses PR review), and `gh release create` depends on the pushed tag. The user pushes from their own shell. Don't try to push or create the GitHub release yourself, even if a previous step succeeded — surface the commands instead.

If no version was given, infer the next one: read the latest tag with `git tag --list 'v*' --sort=-v:refname | head -1`, fall back to the `version` constant in `cmd/lazyduckdb/main.go` if there are no tags, and bump the patch component (`v0.1` → `v0.1.1`, `v0.2.3` → `v0.2.4`). If the user gave a version, use it verbatim — don't second-guess major/minor bumps.

Steps, in this order:

1. **Pre-flight**: `git status --porcelain` must be empty and the current branch must be `master` (the repo's default — see `git status` at session start). Refuse otherwise; tell the user what's dirty.
2. **Bump the version constant** in `cmd/lazyduckdb/main.go` — the `version` var. Strip the leading `v` (the constant stores `0.2.0`, the tag is `v0.2.0`). The update-check in `internal/update` compares against this string, so the two must stay in lockstep.
3. **Build and test**: `go build ./... && go test ./...`. Stop on failure.
4. **Commit**: stage only `cmd/lazyduckdb/main.go`, message `release vX.Y.Z`. Use the heredoc form with the standard `Co-Authored-By` trailer.
5. **Tag**: `git tag -a vX.Y.Z -m "vX.Y.Z"` (annotated, not lightweight — `gh release` and `go install @vX.Y.Z` both prefer annotated).
6. **Hand off**: print exactly these two lines for the user to run themselves:

   ```
   git push github master && git push github vX.Y.Z
   gh release create vX.Y.Z --generate-notes --title "vX.Y.Z"
   ```

   Mention they can verify afterwards with `gh release view vX.Y.Z`. The remote is named `github`, not `origin` (see `git remote -v`).

Don't `git tag -d` or `git reset` to "undo" a release commit — if the user wants to abandon, they can drop the tag and reset themselves; the release flow leaves the working tree at a publishable state and that's the contract. Never use `--force` on a tag push, and don't amend the release commit after the user has pushed — cut a new patch instead.
