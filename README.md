# lazyduckdb

A terminal UI for querying Parquet files with DuckDB. Pass it a `.parquet` file (or let it pick one from the current directory), write SQL against the view `t`, and scroll the results horizontally. Export to `.xlsx` when you're done.

## Install

### With `go install` (recommended)

Requires Go 1.25+.

```sh
go install github.com/svenclaesson/lazyduckdb/cmd/lazyduckdb@latest
```

This drops the binary in `$(go env GOPATH)/bin`. Make sure that's on your `PATH`:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Pin a specific release instead of `@latest`:

```sh
go install github.com/svenclaesson/lazyduckdb/cmd/lazyduckdb@v0.1.0
```

### From source

```sh
git clone https://github.com/svenclaesson/lazyduckdb
cd lazyduckdb
go install ./cmd/lazyduckdb
```

To bake a version string into the binary (e.g. from `git describe`):

```sh
go install -ldflags "-X main.version=$(git describe --tags --always --dirty)" ./cmd/lazyduckdb
```

### Verify

```sh
which lazyduckdb
lazyduckdb -v
```

## Usage

```sh
lazyduckdb path/to/file.parquet
```

With no argument, `lazyduckdb` lists `*.parquet` files in the current directory and lets you pick one. If there's exactly one, it's opened directly.

The file is exposed as a DuckDB view named `t`, so queries look like:

```sql
select * from t limit 100
```

### Multiple files

Pass several files to mount each one as `t1`, `t2`, … — handy for cross-file joins:

```sh
lazyduckdb sales.parquet inventory.parquet
```

```sql
select * from t1 join t2 on t1.sku = t2.sku
```

When the file list looks like a single shell glob expansion (same directory plus a common basename prefix or suffix of two characters or more), the files are folded into one unioned view instead — query them as `t` or `t1`:

```sh
lazyduckdb Ford*.parquet
```

```sql
select count(*) from t   -- every Ford*.parquet row, combined
```

You can also pass the glob quoted so the shell doesn't expand it; DuckDB's `read_parquet` does the expansion internally:

```sh
lazyduckdb 'data/2024-*.parquet'
```

In the no-arg picker, `Ctrl+A` selects every visible file and commits them as a single group with the same unioned-`t` semantics.

## Scope and limitations

lazyduckdb is intended for quickly exploring local Parquet files, not as a general-purpose DuckDB frontend.

What works today:

- Opening one or more local `.parquet` files, either passed explicitly (mounted as `t1`, `t2`, …) or as a shell glob / quoted glob (folded into a single unioned `t`). The picker can also commit a multi-file group via `Ctrl+A`.
- Running arbitrary `SELECT` queries against `t` / `t1` … (and any DuckDB SQL that doesn't depend on the features below).

Not supported yet:

- Hive-partitioned datasets (`hive_partitioning=1`).
- Remote Parquet over HTTP(S), S3, GCS, Azure, or HuggingFace.
- Custom `read_parquet` options (schema overrides, `union_by_name`, `filename`, encryption, etc.).
- Attaching additional tables, views, or databases alongside `t`.
- Writing Parquet (`COPY ... TO`) or persisting to a `.duckdb` file.
- Extensions (`spatial`, `json`, `httpfs`, …) beyond what the embedded DuckDB loads by default.

If you need any of those, drop into the `duckdb` CLI directly — lazyduckdb is deliberately the "open one file and poke at it" tool.

## Keybindings

lazyduckdb targets macOS and registers both Cmd (`super`) and Ctrl forms of every action so it works whether or not your terminal forwards the Kitty keyboard protocol.

| Action | Keys |
| --- | --- |
| Run query (auto-focuses results) | `⌘R` / `Ctrl+R` |
| Export to Excel | `⌘E` / `Ctrl+E` |
| Attach another parquet (as `t2`, `t3`, …) | `⌘O` / `Ctrl+O` |
| Toggle editor ↔ results | `Esc` (or `Ctrl+Q` from results) |
| Search inside results | `/` (Enter for next match, `Esc` to exit) |
| Word left / right | `⌥←` / `⌥→` (or `⌥B` / `⌥F`) |
| Line start / end | `Home` / `End` |
| Help | `Ctrl+H` / `F1` |
| Quit | `Ctrl+C` |

If `⌘`-chords don't work in your terminal, fall back to the `Ctrl` form — or enable the Kitty keyboard protocol (Ghostty, Kitty, WezTerm, and recent iTerm2 support it). `⌘Q` is intentionally unbound because macOS reserves it for "Quit Application" before any terminal app can see it.

## Releases

Releases are tagged with semver (`v0.1.0`, `v0.2.0`, …). Installing via `go install ...@vX.Y.Z` gives you a reproducible build; `@latest` tracks the newest tag.
