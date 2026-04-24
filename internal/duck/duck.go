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

// Session is an in-memory DuckDB connection with a single parquet file
// exposed as a view named "t".
type Session struct {
	db          *sql.DB
	ParquetPath string
	Columns     []Column
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

// Open loads the parquet file and introspects its schema.
func Open(parquetPath string) (*Session, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}

	// Quoting single quotes in the path avoids injection on exotic filenames.
	escaped := strings.ReplaceAll(parquetPath, "'", "''")
	createView := fmt.Sprintf("CREATE VIEW t AS SELECT * FROM read_parquet('%s')", escaped)
	if _, err := db.Exec(createView); err != nil {
		db.Close()
		return nil, fmt.Errorf("read parquet: %w", err)
	}

	cols, err := describe(db)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Session{db: db, ParquetPath: parquetPath, Columns: cols}, nil
}

func describe(db *sql.DB) ([]Column, error) {
	rows, err := db.Query("DESCRIBE SELECT * FROM t")
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
// limit of 0 means "no cap — read everything". Regardless of the cap
// we also run a separate COUNT(*) pass so the caller knows the full
// size of the result. The count is best-effort: if the query isn't
// wrappable as a subquery (EXPLAIN, DDL, etc.), TotalRows is set to
// -1 and no error is raised.
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
	for rows.Next() {
		if limit > 0 && read >= limit {
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

	// Best-effort total-row count. Safe to ignore errors — not every
	// statement is a SELECT we can wrap in a subquery.
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

// ColumnNames returns just the column identifiers — useful for the
// autocomplete dictionary.
func (s *Session) ColumnNames() []string {
	names := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		names[i] = c.Name
	}
	return names
}
