package table

import "testing"

func TestDownAdvancesCursor(t *testing.T) {
	m := New()
	m.SetSize(200, 20) // visible ≈ 17 data rows
	m.Focus()
	rows := make([][]string, 0, 100)
	for i := 0; i < 100; i++ {
		rows = append(rows, []string{"x"})
	}
	m.SetData([]string{"c"}, rows)
	if m.cursorRow != 0 {
		t.Fatalf("initial cursorRow: want 0, got %d", m.cursorRow)
	}
	m.HandleKey("down")
	if m.cursorRow != 1 {
		t.Fatalf("after one down: want cursorRow=1, got %d", m.cursorRow)
	}
	if m.rowOffset != 0 {
		t.Fatalf("one down should not scroll viewport yet; rowOffset=%d", m.rowOffset)
	}
	m.HandleKey("down")
	if m.cursorRow != 2 || m.rowOffset != 0 {
		t.Fatalf("after two downs: cursor=%d offset=%d, want 2/0", m.cursorRow, m.rowOffset)
	}
}

func TestSearchMatchesHeaderOnly(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Focus()
	m.SetData(
		[]string{"Antal", "Datum"},
		[][]string{
			{"5", "2024-01-01"},
			{"9", "2024-01-02"},
		},
	)
	m.HandleText("/")
	for _, ch := range "antal" {
		m.HandleText(string(ch))
	}
	// No cell contains "antal" — but the column header does, so the
	// match list should still be non-empty.
	if len(m.searchMatches) == 0 {
		t.Fatal("expected header-only match to register")
	}
	if m.searchMatches[0].row != -1 {
		t.Fatalf("first match should be the header (row=-1), got %v", m.searchMatches[0])
	}
	// Jumping to a header match pans the column but leaves data
	// cursor at 0 (no meaningful data row for a header hit).
	if m.cursorRow != 0 {
		t.Fatalf("data cursor should stay at 0 for header hit, got %d", m.cursorRow)
	}
	if m.colOffset != 0 {
		t.Fatalf("column 0 should be in view, colOffset=%d", m.colOffset)
	}
}

func TestSearchFlow(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Focus()
	m.SetData(
		[]string{"id", "name"},
		[][]string{
			{"1", "alpha"},
			{"2", "bravo"},
			{"3", "alphabetical"},
			{"4", "charlie"},
		},
	)

	// "/" enters search mode.
	if !m.HandleText("/") {
		t.Fatal(`"/" should enter search mode`)
	}
	if !m.IsSearching() {
		t.Fatal("expected searching=true after /")
	}

	// Type "alp" — matches row 0 and row 2 on the "name" column.
	for _, ch := range "alp" {
		m.HandleText(string(ch))
	}
	if len(m.searchMatches) != 2 {
		t.Fatalf("want 2 matches for 'alp', got %d: %v", len(m.searchMatches), m.searchMatches)
	}
	if m.cursorRow != 0 {
		t.Fatalf("cursor should be on first match (row 0), got %d", m.cursorRow)
	}

	// Enter → next match (row 2).
	m.HandleKey("enter")
	if m.cursorRow != 2 {
		t.Fatalf("cursor should be on second match (row 2), got %d", m.cursorRow)
	}

	// Enter again → wraps to first match (row 0).
	m.HandleKey("enter")
	if m.cursorRow != 0 {
		t.Fatalf("cursor should wrap to row 0, got %d", m.cursorRow)
	}

	// Backspace narrows query; then esc cancels.
	m.HandleKey("backspace") // "al"
	if m.searchQuery != "al" {
		t.Fatalf("want 'al', got %q", m.searchQuery)
	}
	m.HandleKey("esc")
	if m.IsSearching() {
		t.Fatal("esc should exit search mode")
	}
	if m.searchQuery != "" {
		t.Fatalf("expected empty query after esc, got %q", m.searchQuery)
	}
}
