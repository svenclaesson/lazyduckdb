package table

import "testing"

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
