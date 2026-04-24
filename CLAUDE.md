# lazyduckdb

A TUI for querying parquet files with DuckDB. Built with Bubble Tea, structured after `~/github/lazyaz` (or its binary at `~/go/bin/lazyaz`) — `cmd/<app>/main.go` entry, feature packages under `internal/`.

## Keybindings: follow macOS conventions

This is primarily a macOS app. Keybindings should feel native. Always register **both** a Cmd form and a Ctrl form for every primary action — the Cmd form is what the user reaches for, the Ctrl form is the fallback for terminals that don't forward Cmd.

| Action | Bind |
| --- | --- |
| Run query | `super+r`, `ctrl+r` |
| Export to Excel | `super+e`, `ctrl+e` |
| Focus editor | `super+q`, `ctrl+q` |
| Focus results | `super+t`, `ctrl+t` |
| Word left | `alt+left`, `alt+b` |
| Word right | `alt+right`, `alt+f` |
| Line start / end | `home`/`end` (add `ctrl+a`/`ctrl+e` if needed) |

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
