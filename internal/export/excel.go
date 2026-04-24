// Package export writes a result set to an .xlsx file and opens it.
package export

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/xuri/excelize/v2"
)

// Excel's hard per-sheet limits. Exceeding the row cap on a single
// sheet is unavoidable for multi-million-row parquet files, so we
// split the data across multiple sheets ("Query_1", "Query_2", ...).
// The column cap is harder to work around — the extra columns are
// dropped with a warning.
const (
	maxExcelCols = 16384   // column XFD
	maxExcelRows = 1048576 // row 1048576
)

// ToExcel writes columns+rows to a temp .xlsx and opens it with the
// platform default handler.
//
// Return values:
//   - path: the .xlsx path on success (empty on hard failure).
//   - note: non-empty when something worth surfacing happened but the
//     file is fine — e.g. the data was legitimately split across sheets
//     because it exceeded Excel's per-sheet row cap. Shown as a neutral
//     info message, not an error.
//   - err: only set on genuine failure (no columns, write error,
//     dropped data that the user should know about, e.g. >16384 cols).
func ToExcel(columns []string, rows [][]string) (path, note string, err error) {
	if len(columns) == 0 {
		return "", "", fmt.Errorf("no columns to export")
	}

	f := excelize.NewFile()
	defer f.Close()

	colCount := len(columns)
	droppedCols := 0
	if colCount > maxExcelCols {
		droppedCols = colCount - maxExcelCols
		colCount = maxExcelCols
	}

	// Build the header once — same row is written to every sheet.
	headerRow := make([]interface{}, colCount)
	for i := 0; i < colCount; i++ {
		headerRow[i] = columns[i]
	}

	// Row 1 is always the header, so each sheet can hold
	// maxExcelRows - 1 data rows.
	const rowsPerSheet = maxExcelRows - 1
	total := len(rows)
	sheetCount := 1
	if total > rowsPerSheet {
		sheetCount = (total + rowsPerSheet - 1) / rowsPerSheet
	}

	for s := 0; s < sheetCount; s++ {
		name := "Query"
		if sheetCount > 1 {
			name = fmt.Sprintf("Query_%d", s+1)
		}
		if s == 0 {
			// Replace the auto-created "Sheet1" with ours.
			idx, nerr := f.NewSheet(name)
			if nerr != nil {
				return "", "", fmt.Errorf("new sheet %q: %w", name, nerr)
			}
			f.SetActiveSheet(idx)
			_ = f.DeleteSheet("Sheet1")
		} else {
			if _, nerr := f.NewSheet(name); nerr != nil {
				return "", "", fmt.Errorf("new sheet %q: %w", name, nerr)
			}
		}

		if werr := f.SetSheetRow(name, "A1", &headerRow); werr != nil {
			return "", "", fmt.Errorf("header in %q: %w", name, werr)
		}

		start := s * rowsPerSheet
		end := start + rowsPerSheet
		if end > total {
			end = total
		}
		for r := start; r < end; r++ {
			excelRow := (r - start) + 2
			cellRef, cerr := excelize.CoordinatesToCellName(1, excelRow)
			if cerr != nil {
				return "", "", fmt.Errorf("coords row %d in %q: %w", excelRow, name, cerr)
			}
			row := rows[r]
			n := len(row)
			if n > colCount {
				n = colCount
			}
			vals := make([]interface{}, n)
			for i := 0; i < n; i++ {
				vals[i] = row[i]
			}
			if werr := f.SetSheetRow(name, cellRef, &vals); werr != nil {
				return "", "", fmt.Errorf("row %d in %q: %w", excelRow, name, werr)
			}
		}
	}

	path = filepath.Join(os.TempDir(),
		fmt.Sprintf("lazyduckdb-%s.xlsx", time.Now().Format("20060102-150405")))
	if serr := f.SaveAs(path); serr != nil {
		return "", "", fmt.Errorf("save: %w", serr)
	}
	if oerr := openFile(path); oerr != nil {
		return path, "", fmt.Errorf("file saved but failed to open: %w", oerr)
	}

	// Dropped columns = actual data loss → surface as err.
	// Sheet split = intentional distribution of complete data → note.
	switch {
	case droppedCols > 0 && sheetCount > 1:
		return path, "", fmt.Errorf("%d rows across %d sheets; %d cols omitted (Excel cap %d cols)",
			total, sheetCount, droppedCols, maxExcelCols)
	case droppedCols > 0:
		return path, "", fmt.Errorf("%d cols omitted (Excel cap %d cols)", droppedCols, maxExcelCols)
	case sheetCount > 1:
		return path, fmt.Sprintf("%d rows split across %d sheets (Excel's per-sheet cap is %d)",
			total, sheetCount, maxExcelRows), nil
	}
	return path, "", nil
}

func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
