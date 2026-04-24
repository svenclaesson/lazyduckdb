package export

import (
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestToExcelRoundTrip(t *testing.T) {
	// Small self-contained sample — verifies that the happy path
	// writes a readable .xlsx with the expected header + data.
	cols := []string{"id", "name", "amount"}
	rows := [][]string{
		{"1", "alice", "10.5"},
		{"2", "bob", "20"},
	}

	path, _, err := ToExcel(cols, rows)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Remove(path)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer f.Close()

	got, err := f.GetCellValue("Query", "A1")
	if err != nil || got != "id" {
		t.Fatalf("A1: got %q err=%v, want 'id'", got, err)
	}
	got, err = f.GetCellValue("Query", "B2")
	if err != nil || got != "alice" {
		t.Fatalf("B2: got %q err=%v, want 'alice'", got, err)
	}
	got, err = f.GetCellValue("Query", "C3")
	if err != nil || got != "20" {
		t.Fatalf("C3: got %q err=%v, want '20'", got, err)
	}
}

func TestToExcelSingleSheetHasNoNoteOrError(t *testing.T) {
	cols := []string{"id"}
	rows := [][]string{{"1"}, {"2"}}
	path, note, err := ToExcel(cols, rows)
	if err != nil {
		t.Fatalf("small export err: %v", err)
	}
	if note != "" {
		t.Fatalf("expected empty note for single-sheet export, got %q", note)
	}
	defer os.Remove(path)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer f.Close()
	names := f.GetSheetList()
	if len(names) != 1 || names[0] != "Query" {
		t.Fatalf("want single 'Query' sheet, got %v", names)
	}
}
