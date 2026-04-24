package editor

import (
	"strings"
	"testing"
)

func TestTabCompletesUniquePrefix(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"customer_id", "customer_name", "order_id"})
	m.Focus()
	for _, ch := range "order_" {
		m.HandleKey(string(ch))
	}
	m.HandleKey("tab")
	if got := m.Value(); got != "order_id" {
		t.Fatalf("expected order_id, got %q", got)
	}
}

func TestEnterAcceptsOpenSuggestion(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"customer_id", "customer_name", "customer_kind"})
	m.Focus()
	for _, ch := range "cust" {
		m.HandleKey(string(ch))
	}
	m.HandleKey("tab") // opens list with 3 matches
	if len(m.suggestions) < 2 {
		t.Fatalf("expected suggestions to be open, got %v", m.suggestions)
	}
	// Matches are sorted alphabetically: customer_id, customer_kind, customer_name.
	// Selection moves with Right/Left, not Up/Down.
	m.HandleKey("right") // → customer_kind
	m.HandleKey("enter") // accept
	if got := m.Value(); got != "customer_kind" {
		t.Fatalf("want customer_kind, got %q", got)
	}
	if len(m.suggestions) != 0 {
		t.Fatal("suggestions should close after accept")
	}
}

func TestTabAcceptsSelectionWhenListOpen(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"alpha", "alpine", "altitude"})
	m.Focus()
	for _, ch := range "al" {
		m.HandleKey(string(ch))
	}
	m.HandleKey("tab")   // opens with 3 matches, idx 0 = alpha
	m.HandleKey("right") // → alpine
	m.HandleKey("tab")   // Tab should accept, not cycle
	if got := m.Value(); got != "alpine" {
		t.Fatalf("tab should accept currently selected 'alpine', got %q", got)
	}
	if len(m.suggestions) != 0 {
		t.Fatal("list should be closed after accept")
	}
}

func TestTabOnEmptyCaretShowsAllOptions(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"alpha", "beta", "gamma"})
	m.Focus()
	m.HandleKey("tab")
	if len(m.suggestions) != 3 {
		t.Fatalf("want 3 suggestions, got %v", m.suggestions)
	}
	// Accepting still works — first item goes in.
	m.HandleKey("enter")
	if m.Value() != "alpha" {
		t.Fatalf("want alpha, got %q", m.Value())
	}
}

func TestTabBetweenWordsShowsAllOptions(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"id", "name", "email"})
	m.Focus()
	m.SetValue("SELECT  FROM t")
	// Place caret between "SELECT " and "FROM" — row 0, col 7.
	m.row = 0
	m.col = 7
	m.HandleKey("tab")
	if len(m.suggestions) != 3 {
		t.Fatalf("expected all dictionary entries, got %v", m.suggestions)
	}
	// Dict sorts to: email, id, name. One Right advances 0 → 1 = "id".
	m.HandleKey("right")
	m.HandleKey("enter")
	if got := m.Value(); got != "SELECT id FROM t" {
		t.Fatalf("want 'SELECT id FROM t', got %q", got)
	}
}

func TestAutoTriggerInsideSelectFrom(t *testing.T) {
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name", "order_id"})
	m.Focus()
	m.SetValue("SELECT  FROM t")
	m.row, m.col = 0, 7 // between "SELECT " and "FROM"
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected auto-trigger with 2 customer_* matches, got %v", m.suggestions)
	}
	// Enter accepts the first — "customer_id".
	m.HandleKey("enter")
	if got := m.Value(); got != "SELECT customer_id FROM t" {
		t.Fatalf("want accepted, got %q", got)
	}
}

func TestAutoTriggerFiresInWhereClause(t *testing.T) {
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name"})
	m.Focus()
	m.SetValue("SELECT * FROM t WHERE ")
	m.row, m.col = 0, len("SELECT * FROM t WHERE ")
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected column list in WHERE, got %v", m.suggestions)
	}
}

func TestTabAcceptsWhenOnlyOneSuggestionOpen(t *testing.T) {
	m := New()
	m.SetColumns([]string{"customer_id", "order_id"})
	m.Focus()
	m.SetValue("SELECT  FROM t")
	m.row, m.col = 0, 7
	m.HandleKey("c") // auto-trigger: 1 match, "customer_id"
	if len(m.suggestions) != 1 {
		t.Fatalf("expected exactly 1 match, got %v", m.suggestions)
	}
	m.HandleKey("tab")
	if got := m.Value(); got != "SELECT customer_id FROM t" {
		t.Fatalf("tab should accept the single option, got %q", got)
	}
}

func TestSuggestionsHideAlreadyUsedColumns(t *testing.T) {
	m := New()
	m.SetColumns([]string{"Regnr", "RegDatum", "RegType"})
	m.SetDictionary([]string{"Regnr", "RegDatum", "RegType"})
	m.Focus()
	// Already used Regnr earlier in the query — typing a second
	// column prefix should not suggest it again.
	m.SetValue("SELECT Regnr, R FROM t")
	m.row, m.col = 0, len("SELECT Regnr, R")
	m.HandleKey("tab")
	for _, s := range m.suggestions {
		if strings.EqualFold(s, "Regnr") {
			t.Fatalf("Regnr should be filtered out (already in query), got %v", m.suggestions)
		}
	}
	if len(m.suggestions) == 0 {
		t.Fatal("expected at least RegDatum/RegType to remain")
	}
}

func TestAlreadyUsedFilterIsCaseInsensitive(t *testing.T) {
	m := New()
	m.SetColumns([]string{"CustomerId", "CustomerName"})
	m.SetDictionary([]string{"CustomerId", "CustomerName"})
	m.Focus()
	m.SetValue("SELECT customerid, c FROM t")
	m.row, m.col = 0, len("SELECT customerid, c")
	m.HandleKey("tab")
	for _, s := range m.suggestions {
		if strings.EqualFold(s, "CustomerId") {
			t.Fatalf("CustomerId should be filtered despite different case, got %v", m.suggestions)
		}
	}
}

func TestAlreadyUsedFilterOffInWhereClause(t *testing.T) {
	// A column present in the SELECT list is a legitimate target for
	// a WHERE predicate — the filter shouldn't hide it there.
	m := New()
	m.SetColumns([]string{"Regnr", "Datum"})
	m.Focus()
	m.SetValue("SELECT Regnr FROM t WHERE R")
	m.row, m.col = 0, len("SELECT Regnr FROM t WHERE R")
	// Simulate an in-flight typing state by opening via refilter path.
	// Typing 'R' auto-triggers; we inserted 'R' via SetValue already,
	// so drive the trigger by calling the same path directly.
	m.HandleKey("") // no-op to ensure we're in consistent state
	// Re-open via auto-trigger by typing another char and removing it.
	m.HandleKey("e")
	m.HandleKey("backspace")
	found := false
	for _, s := range m.suggestions {
		if s == "Regnr" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Regnr should be offered in WHERE even though it's in SELECT, got %v", m.suggestions)
	}
}

func TestAlreadyUsedFilterActiveInSelectList(t *testing.T) {
	m := New()
	m.SetColumns([]string{"Regnr", "RegDatum"})
	m.Focus()
	m.SetValue("SELECT Regnr, R FROM t")
	m.row, m.col = 0, len("SELECT Regnr, R")
	m.HandleKey("e") // triggers auto-trigger in SELECT list
	for _, s := range m.suggestions {
		if s == "Regnr" {
			t.Fatalf("Regnr should be filtered out in SELECT list, got %v", m.suggestions)
		}
	}
}

func TestTypingNarrowsOpenSuggestionList(t *testing.T) {
	m := New()
	m.SetDictionary([]string{"customer_id", "customer_name", "order_id", "order_date"})
	m.Focus()
	// Open full list on empty caret.
	m.HandleKey("tab")
	if len(m.suggestions) != 4 {
		t.Fatalf("expected 4, got %v", m.suggestions)
	}
	// Type "c" — list should narrow to the two customer_* entries.
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected 2 customer_* entries after 'c', got %v", m.suggestions)
	}
	// Backspace should widen back to the full 4.
	m.HandleKey("backspace")
	if len(m.suggestions) != 4 {
		t.Fatalf("expected full list after backspace, got %v", m.suggestions)
	}
	// Type something that doesn't match → list becomes empty.
	m.HandleKey("z")
	if len(m.suggestions) != 0 {
		t.Fatalf("expected empty list after 'z', got %v", m.suggestions)
	}
}

func TestBackspaceReopensListAfterNoMatchTyping(t *testing.T) {
	m := New()
	m.SetColumns([]string{"Regnr", "RegDatum"})
	m.Focus()
	m.SetValue("SELECT  FROM t")
	m.row, m.col = 0, 7 // inside SELECT..FROM
	m.HandleKey("r")
	m.HandleKey("e")
	m.HandleKey("g")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected 2 matches at 'reg', got %v", m.suggestions)
	}
	// Typing another 'g' → "regg", no dictionary entries match.
	m.HandleKey("g")
	if len(m.suggestions) != 0 {
		t.Fatalf("expected 0 matches at 'regg', got %v", m.suggestions)
	}
	// Backspace should widen back to 'reg' and re-show the two entries.
	m.HandleKey("backspace")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected list to recover on backspace, got %v", m.suggestions)
	}
}

func TestLeftRightMoveCaretWhenNoSuggestions(t *testing.T) {
	m := New()
	m.Focus()
	m.SetValue("abc")
	m.HandleKey("left")
	if m.col != 2 {
		t.Fatalf("left with no suggestions should move caret; col=%d", m.col)
	}
}

func TestEnterInsertsNewlineWhenNoSuggestions(t *testing.T) {
	m := New()
	m.Focus()
	m.HandleKey("a")
	m.HandleKey("enter")
	m.HandleKey("b")
	if got := m.Value(); got != "a\nb" {
		t.Fatalf("want a\\nb, got %q", got)
	}
}

func TestMatchPrefixCaseInsensitive(t *testing.T) {
	got := matchPrefix([]string{"Alpha", "alpine", "Beta"}, "al")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %v", got)
	}
}

func TestWordNavigation(t *testing.T) {
	m := New()
	m.Focus()
	m.SetValue("SELECT id FROM users")
	// Caret is at end (col 20). alt+left should jump to start of "users" (col 15).
	m.HandleKey("alt+left")
	if m.col != 15 {
		t.Fatalf("alt+left 1: want col 15, got %d", m.col)
	}
	m.HandleKey("alt+left") // → start of "FROM" (col 10)
	if m.col != 10 {
		t.Fatalf("alt+left 2: want col 10, got %d", m.col)
	}
	m.HandleKey("alt+left") // → start of "id" (col 7)
	if m.col != 7 {
		t.Fatalf("alt+left 3: want col 7, got %d", m.col)
	}
	m.HandleKey("alt+right") // → start of "FROM" (col 10)
	if m.col != 10 {
		t.Fatalf("alt+right 1: want col 10, got %d", m.col)
	}
	m.HandleKey("alt+right") // → start of "users" (col 15)
	if m.col != 15 {
		t.Fatalf("alt+right 2: want col 15, got %d", m.col)
	}
}

// macOS Terminal sends Option+Left as ESC-b (readline "backward-word"),
// which bubbletea reports as alt+b. Same for Option+Right → alt+f.
func TestWordNavigationReadlineAliases(t *testing.T) {
	m := New()
	m.Focus()
	m.SetValue("SELECT id FROM users")
	m.HandleKey("alt+b")
	if m.col != 15 {
		t.Fatalf("alt+b: want col 15, got %d", m.col)
	}
	m.HandleKey("alt+f")
	if m.col != 20 {
		t.Fatalf("alt+f: want col 20, got %d", m.col)
	}
}
