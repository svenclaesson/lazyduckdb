// Package duck wraps DuckDB operations for a single parquet-backed session.
package duck

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	_ "github.com/marcboeker/go-duckdb/v2"
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

// Source is a single parquet file mapped to a view inside the
// session. View is the SQL identifier (t1, t2, ...).
type Source struct {
	View    string
	Path    string
	Columns []Column
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

// Open loads the parquet file as views "t1" and "t" (alias) and
// introspects its schema.
func Open(parquetPath string) (*Session, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}

	escaped := strings.ReplaceAll(parquetPath, "'", "''")
	// "t1" is the canonical name; "t" is a one-source convenience
	// alias so the default "SELECT * FROM t" template keeps working.
	for _, view := range []string{"t1", "t"} {
		stmt := fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_parquet('%s')", view, escaped)
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("read parquet: %w", err)
		}
	}

	cols, err := describe(db, "t1")
	if err != nil {
		db.Close()
		return nil, err
	}

	src := Source{View: "t1", Path: parquetPath, Columns: cols}
	return &Session{
		db:          db,
		Sources:     []Source{src},
		ParquetPath: parquetPath,
		Columns:     cols,
	}, nil
}

// Attach adds another parquet under the next available tN view name
// and refreshes the session's column metadata. Returns the view name
// it was bound to, or an error if the path can't be opened.
func (s *Session) Attach(parquetPath string) (string, error) {
	view := fmt.Sprintf("t%d", len(s.Sources)+1)
	escaped := strings.ReplaceAll(parquetPath, "'", "''")
	stmt := fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_parquet('%s')", view, escaped)
	if _, err := s.db.Exec(stmt); err != nil {
		return "", fmt.Errorf("attach %s: %w", parquetPath, err)
	}
	cols, err := describe(s.db, view)
	if err != nil {
		// Best effort: leave the view in place but report the error
		// — describe failure usually means the file is unreadable,
		// which the user will discover the moment they query.
		return view, err
	}
	s.Sources = append(s.Sources, Source{View: view, Path: parquetPath, Columns: cols})
	return view, nil
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
