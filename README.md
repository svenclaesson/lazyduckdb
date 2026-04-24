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

## Scope and limitations

lazyduckdb currently exposes only a very small subset of DuckDB's Parquet capabilities. It's intended for quickly exploring a single local file, not as a general-purpose DuckDB frontend.

What works today:

- Opening **one** local `.parquet` file, exposed as a DuckDB view named `t`.
- Running arbitrary `SELECT` queries against `t` (and any DuckDB SQL that doesn't depend on the features below).

Not supported yet:

- Multiple files, globs, or directories (`read_parquet(['a.parquet', 'b.parquet'])`, `read_parquet('data/*.parquet')`).
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
| Run query | `⌘R` / `Ctrl+R` |
| Export to Excel | `⌘E` / `Ctrl+E` |
| Focus editor | `⌘Q` / `Ctrl+Q` |
| Focus results | `⌘T` / `Ctrl+T` |
| Word left / right | `⌥←` / `⌥→` (or `⌥B` / `⌥F`) |
| Line start / end | `Home` / `End` |
| Quit | `Ctrl+C` |

If `⌘`-chords don't work in your terminal, fall back to the `Ctrl` form — or enable the Kitty keyboard protocol (Ghostty, Kitty, WezTerm, and recent iTerm2 support it).

## Releases

Releases are tagged with semver (`v0.1.0`, `v0.2.0`, …). Installing via `go install ...@vX.Y.Z` gives you a reproducible build; `@latest` tracks the newest tag.
