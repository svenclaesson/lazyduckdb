// Package duck wraps DuckDB operations for a single parquet-backed session.
package duck

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "github.com/marcboeker/go-duckdb/v2"
)

// Group-attach guard rails. These cap how much parquet a single
// grouped mount can pull in, so a too-eager wildcard (`*` against a
// directory full of large files) fails fast with a clear message
// instead of silently dragging the host into swap. Both defaults can
// be overridden per-run via env vars — the user has already opted
// into "I know what I'm doing" by setting them.
//
//   LAZYDUCKDB_GROUP_MAX_FILES — int, default 500
//   LAZYDUCKDB_GROUP_MAX_BYTES — bytes, default 10 GiB
//
// The byte cap is on-disk parquet size summed across the group, not
// in-memory expanded size. Compressed columnar data routinely
// expands 5-10× when materialized, which is why the default is
// deliberately conservative.
const (
	defaultGroupMaxFiles = 500
	defaultGroupMaxBytes = 10 * 1024 * 1024 * 1024 // 10 GiB
)

// Session is an in-memory DuckDB connection with one or more parquet
// files exposed as views t1, t2, ... The first source is also aliased
// as plain "t" so the default `SELECT * FROM t` keeps working when
// only one file is loaded.
type Session struct {
	db      *sql.DB
	Sources []Source

	// ParquetPath / Columns are kept for backward compatibility with
	// callers that pre-date multi-source support. They mirror the
	// first attached source.
	ParquetPath string
	Columns     []Column
}

// Source is a single parquet file (or glob-matched group of files)
// mapped to a view inside the session. View is the SQL identifier
// (t1, t2, ...). For a single-file source Path is the absolute path
// and Files/Glob are empty. For a globbed source Path is the glob
// pattern (also stored in Glob), and Files lists every parquet the
// glob expanded to at attach time.
type Source struct {
	View    string
	Path    string
	Columns []Column

	// Glob is the original glob pattern when this source was attached
	// via wildcard (e.g. "*test*.parquet"); empty for single-file sources.
	Glob string
	// Files is the expansion of Glob at attach time. Empty for
	// single-file sources. Used by the UI to show the file count.
	Files []string
}

// IsGroup reports whether this source is a glob-backed union of
// multiple parquet files rather than a single file.
func (s Source) IsGroup() bool { return s.Glob != "" }

// IsGlob reports whether the path contains shell-glob metacharacters.
// We use this to decide between read_parquet('one.parquet') and the
// union form read_parquet('pattern', union_by_name=true, filename=true).
func IsGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

type Column struct {
	Name string
	Type string
}

// ResultSet is a fully materialized query result. Rows are stored as
// strings because they end up rendered in the table cell-by-cell and
// exported to Excel — carrying typed values through the pipeline
// would add a lot of machinery for no real gain.
//
// TotalRows is the full row count of the query as reported by a
// separate COUNT(*) pass; it can be larger than len(Rows) when a
// display cap was applied. A value of -1 means the count could not
// be computed (e.g. the query isn't a plain SELECT).
type ResultSet struct {
	Columns   []string
	Rows      [][]string
	TotalRows int
}

// Open loads the parquet file (or glob) as views "t1" and "t" (alias)
// and introspects its schema. When parquetPath contains glob meta the
// view is created via read_parquet('<glob>', union_by_name=true,
// filename=true) so heterogeneous schemas merge gracefully and each
// row carries its source filename.
func Open(parquetPath string) (*Session, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}

	src, err := createSourceView(db, "t1", parquetPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	// "t" is a one-source convenience alias so the default
	// "SELECT * FROM t" template keeps working.
	if _, err := db.Exec(buildReadParquetView("t", parquetPath)); err != nil {
		db.Close()
		return nil, fmt.Errorf("read parquet: %w", err)
	}

	return &Session{
		db:          db,
		Sources:     []Source{src},
		ParquetPath: parquetPath,
		Columns:     src.Columns,
	}, nil
}

// Attach adds another parquet (or parquet glob) under the next
// available tN view name and refreshes the session's column metadata.
// Returns the view name it was bound to, or an error if the path
// can't be opened.
func (s *Session) Attach(parquetPath string) (string, error) {
	view := fmt.Sprintf("t%d", len(s.Sources)+1)
	src, err := createSourceView(s.db, view, parquetPath)
	if err != nil {
		return "", err
	}
	s.Sources = append(s.Sources, src)
	return view, nil
}

// OpenGroup loads an explicit list of parquet files as a single
// unioned view, using `label` as the display path (e.g. the glob
// pattern that produced the list). Use this instead of Open when
// you already know the resolved file list — it avoids any glob
// case-sensitivity / cwd-resolution surprises since the file paths
// are passed verbatim to read_parquet.
func OpenGroup(label string, files []string) (*Session, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("open group %q: no files", label)
	}
	if err := CheckGroupSize(files); err != nil {
		return nil, err
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	src, err := createGroupView(db, "t1", label, files)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(buildReadParquetGroupView("t", files)); err != nil {
		db.Close()
		return nil, fmt.Errorf("read parquet group: %w", err)
	}
	return &Session{
		db:          db,
		Sources:     []Source{src},
		ParquetPath: label,
		Columns:     src.Columns,
	}, nil
}

// AttachGroup is the multi-file analogue of Attach — see OpenGroup.
func (s *Session) AttachGroup(label string, files []string) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("attach group %q: no files", label)
	}
	if err := CheckGroupSize(files); err != nil {
		return "", err
	}
	view := fmt.Sprintf("t%d", len(s.Sources)+1)
	src, err := createGroupView(s.db, view, label, files)
	if err != nil {
		return "", err
	}
	s.Sources = append(s.Sources, src)
	return view, nil
}

// createGroupView creates a unioned view from an explicit list of
// parquet files and returns a populated group Source. The label is
// stored as both Path and Glob so the existing rendering branches
// (IsGroup() etc.) continue to work.
func createGroupView(db *sql.DB, view, label string, files []string) (Source, error) {
	if _, err := db.Exec(buildReadParquetGroupView(view, files)); err != nil {
		return Source{}, fmt.Errorf("attach group %q: %w", label, err)
	}
	cols, err := describe(db, view)
	src := Source{
		View:    view,
		Path:    label,
		Columns: cols,
		Glob:    label,
		Files:   append([]string(nil), files...),
	}
	if err != nil {
		return src, err
	}
	return src, nil
}

// CheckGroupSize enforces the file-count and total-byte caps for a
// grouped mount. Returns a descriptive error when either cap is
// exceeded so the user sees concrete numbers (and the override env
// vars) instead of a generic "too big". Stat failures on individual
// files are tolerated — they just don't contribute to the total —
// because read_parquet will produce its own clearer error if a path
// turns out to be unreadable.
func CheckGroupSize(files []string) error {
	maxFiles := envInt("LAZYDUCKDB_GROUP_MAX_FILES", defaultGroupMaxFiles)
	maxBytes := envInt64("LAZYDUCKDB_GROUP_MAX_BYTES", defaultGroupMaxBytes)
	if len(files) > maxFiles {
		return fmt.Errorf(
			"refusing to open %d files (cap: %d). narrow the picker filter or override with LAZYDUCKDB_GROUP_MAX_FILES",
			len(files), maxFiles)
	}
	var total int64
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		total += fi.Size()
		if total > maxBytes {
			return fmt.Errorf(
				"refusing to open group: combined size > %s (cap: %s). narrow the picker filter or override with LAZYDUCKDB_GROUP_MAX_BYTES",
				humanBytes(total), humanBytes(maxBytes))
		}
	}
	return nil
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// sourceFileColumn is the column name DuckDB writes the per-row
// source-file path into. Picked to be unlikely to collide with a
// real user column — "filename" is too common (some parquets ship
// it as a real field, which collides with read_parquet's default).
const sourceFileColumn = "_source_file"

// buildReadParquetGroupView returns the SQL that mounts an explicit
// list of parquet files as a unioned view. Always uses
// union_by_name=true and a custom filename column so heterogeneous
// schemas merge and rows stay traceable to their source file
// without colliding with a real "filename" column.
func buildReadParquetGroupView(view string, files []string) string {
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = "'" + strings.ReplaceAll(f, "'", "''") + "'"
	}
	return fmt.Sprintf(
		"CREATE VIEW %s AS SELECT * FROM read_parquet([%s], union_by_name=true, filename='%s')",
		view, strings.Join(quoted, ", "), sourceFileColumn)
}

// createSourceView creates the SQL view for a single source and
// returns the populated Source struct. Handles both literal paths
// and globs — for a glob it also pre-expands the file list so the
// UI can show how many files are unioned.
func createSourceView(db *sql.DB, view, parquetPath string) (Source, error) {
	if _, err := db.Exec(buildReadParquetView(view, parquetPath)); err != nil {
		return Source{}, fmt.Errorf("attach %s: %w", parquetPath, err)
	}
	cols, err := describe(db, view)
	src := Source{View: view, Path: parquetPath, Columns: cols}
	if IsGlob(parquetPath) {
		src.Glob = parquetPath
		matches, gerr := filepath.Glob(parquetPath)
		if gerr == nil {
			sort.Strings(matches)
			src.Files = matches
		}
	}
	if err != nil {
		// Best effort: leave the view in place but report the error
		// — describe failure usually means the source is unreadable,
		// which the user will discover the moment they query.
		return src, err
	}
	return src, nil
}

// buildReadParquetView returns the SQL that maps a parquet path or
// glob to the given view name. Globs get union_by_name + a custom
// filename column so schema differences across files don't error
// out and each row stays traceable to its source file.
func buildReadParquetView(view, parquetPath string) string {
	escaped := strings.ReplaceAll(parquetPath, "'", "''")
	if IsGlob(parquetPath) {
		return fmt.Sprintf(
			"CREATE VIEW %s AS SELECT * FROM read_parquet('%s', union_by_name=true, filename='%s')",
			view, escaped, sourceFileColumn)
	}
	return fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_parquet('%s')", view, escaped)
}

func describe(db *sql.DB, view string) ([]Column, error) {
	rows, err := db.Query("DESCRIBE SELECT * FROM " + view)
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// DESCRIBE returns: column_name, column_type, null, key, default, extra.
	// We only need the first two, but scan everything so we don't depend
	// on column order being stable across DuckDB versions.
	var out []Column
	for rows.Next() {
		scanTargets := make([]any, len(colNames))
		holders := make([]sql.NullString, len(colNames))
		for i := range holders {
			scanTargets[i] = &holders[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return nil, err
		}
		col := Column{}
		for i, name := range colNames {
			switch strings.ToLower(name) {
			case "column_name":
				col.Name = holders[i].String
			case "column_type":
				col.Type = holders[i].String
			}
		}
		if col.Name != "" {
			out = append(out, col)
		}
	}
	return out, rows.Err()
}

// Query runs a SQL statement and materializes up to `limit` rows. A
// limit of 0 means "no cap — read everything". When the cap was hit
// we run a separate COUNT(*) pass so the caller knows the full size
// of the result; otherwise the natural exhaustion of the iterator is
// the true count and we skip the second pass entirely. The count is
// best-effort: if the query isn't wrappable as a subquery (EXPLAIN,
// DDL, etc.), TotalRows is set to -1 and no error is raised.
func (s *Session) Query(sqlText string, limit int) (*ResultSet, error) {
	rows, err := s.db.Query(sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	rs := &ResultSet{Columns: cols, TotalRows: -1}
	read := 0
	hitLimit := false
	for rows.Next() {
		if limit > 0 && read >= limit {
			hitLimit = true
			break
		}
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range holders {
			row[i] = formatValue(v)
		}
		rs.Rows = append(rs.Rows, row)
		read++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// If we drained the iterator naturally, `read` is the exact total
	// — no need to re-execute the (potentially expensive) user query
	// just to count its output. Only when we broke early because of
	// the cap do we fall back to the COUNT(*) wrap.
	if !hitLimit {
		rs.TotalRows = read
		return rs, nil
	}
	// Close the open rows first so countRows doesn't have to compete
	// with us for a connection (and so it isn't holding two cursors
	// open against the same db when DuckDB picks a second pool conn).
	rows.Close()
	if total, err := s.countRows(sqlText); err == nil {
		rs.TotalRows = total
	}
	return rs, nil
}

func (s *Session) countRows(sqlText string) (int, error) {
	// Trim trailing semicolon so the subquery wrap is syntactically valid.
	trimmed := strings.TrimRight(strings.TrimSpace(sqlText), ";")
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM (" + trimmed + ")").Scan(&total)
	return total, err
}

func formatValue(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	case string:
		return val
	case float64:
		return formatFloat(val)
	case float32:
		return formatFloat(float64(val))
	default:
		return fmt.Sprintf("%v", val)
	}
}

// formatFloat renders a DuckDB-sourced floating-point value without
// Go's default scientific notation for large magnitudes. When the
// value is a whole number (e.g. a date stored as 20200825.0 from a
// DOUBLE column) it's shown as an integer; non-whole values fall
// back to the minimum digits needed to round-trip.
func formatFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	if f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (s *Session) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ColumnNames returns the deduped union of column identifiers across
// every attached source — used for the autocomplete dictionary.
// Case-insensitive dedup, original casing preserved on first sight.
func (s *Session) ColumnNames() []string {
	seen := make(map[string]struct{})
	var names []string
	for _, src := range s.Sources {
		for _, c := range src.Columns {
			k := strings.ToLower(c.Name)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			names = append(names, c.Name)
		}
	}
	return names
}
