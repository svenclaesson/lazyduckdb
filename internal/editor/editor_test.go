package editor

import (
	"strings"
	"testing"
)

func TestNonASCIIRunesAreInserted(t *testing.T) {
	// Regression: HandleKey used to gate rune insertion on `len(key) == 1`,
	// which is byte length — so multi-byte UTF-8 runes ("é", "ö", "🦆")
	// were silently dropped on the floor. model.go calls HandleKey once
	// per rune (`for _, r := range msg.Text { HandleKey(string(r)) }`),
	// so each call's `key` is exactly one rune, possibly multi-byte.
	m := New()
	m.Focus()
	for _, r := range "naïve 🦆" {
		m.HandleKey(string(r))
	}
	if got := m.Value(); got != "naïve 🦆" {
		t.Fatalf("expected non-ASCII runes to be inserted verbatim, got %q", got)
	}
}

func TestInvalidUTF8IsRejected(t *testing.T) {
	// A two-rune string (or invalid UTF-8) must not be inserted as a
	// single insertRune call — the contract is "exactly one rune".
	m := New()
	m.Focus()
	m.HandleKey("ab")        // two ASCII runes
	m.HandleKey("\xff\xfe")  // invalid UTF-8
	if got := m.Value(); got != "" {
		t.Fatalf("expected multi-rune / invalid input to be ignored, got %q", got)
	}
}

func TestDeleteRemovesCharToTheRight(t *testing.T) {
	// The forward-delete key (fn+Delete on macOS, "Del" on full
	// keyboards) arrives as the string "delete". It must remove the
	// rune to the right of the caret without moving the caret.
	m := New()
	m.Focus()
	m.SetValue("SELECT")
	m.row, m.col = 0, 0
	if !m.HandleKey("delete") {
		t.Fatalf("delete should be consumed by the editor")
	}
	if got := m.Value(); got != "ELECT" {
		t.Fatalf("delete should remove 'S', got %q", got)
	}
	if m.col != 0 {
		t.Fatalf("delete should not move the caret, col=%d", m.col)
	}
}

func TestDeleteAtLineEndJoinsNextLine(t *testing.T) {
	// At end-of-line, forward-delete pulls the following line up,
	// mirroring backspace's line-join at column 0.
	m := New()
	m.Focus()
	m.SetValue("ab\ncd")
	m.row, m.col = 0, 2 // end of "ab"
	m.HandleKey("delete")
	if got := m.Value(); got != "abcd" {
		t.Fatalf("delete at line end should join next line, got %q", got)
	}
	// At the very end of the buffer it's a no-op.
	m.row, m.col = 0, 4
	m.HandleKey("delete")
	if got := m.Value(); got != "abcd" {
		t.Fatalf("delete at buffer end should be a no-op, got %q", got)
	}
}

func TestAltDeleteDeletesWordForward(t *testing.T) {
	// Forward word-delete: Option+fn+Delete arrives as "alt+delete"
	// (and the readline alias "alt+d"). Mirrors moveWordRight — it
	// chews the word to the right of the caret without moving it.
	m := New()
	m.Focus()
	m.SetValue("SELECT * FROM customers")
	m.row, m.col = 0, 0
	m.HandleKey("alt+delete")
	if got := m.Value(); got != " * FROM customers" {
		t.Fatalf("alt+delete should delete 'SELECT', got %q", got)
	}
	// Leading separators plus the next word go together (here " * FROM"),
	// mirroring how alt+backspace bundles a separator with its word.
	m.HandleKey("alt+delete")
	if got := m.Value(); got != " customers" {
		t.Fatalf("alt+delete should delete ' * FROM', got %q", got)
	}
}

func TestAltDDeletesWordForward(t *testing.T) {
	// alt+d is the readline forward-kill-word fallback.
	m := New()
	m.Focus()
	m.SetValue("one two three")
	m.row, m.col = 0, 0
	m.HandleKey("alt+d")
	if got := m.Value(); got != " two three" {
		t.Fatalf("alt+d should delete 'one', got %q", got)
	}
}

func TestAltBackspaceDeletesWord(t *testing.T) {
	m := New()
	m.Focus()
	m.SetValue("SELECT * FROM customers")
	m.row, m.col = 0, len("SELECT * FROM customers")
	m.HandleKey("alt+backspace")
	if got := m.Value(); got != "SELECT * FROM " {
		t.Fatalf("alt+backspace should delete 'customers', got %q", got)
	}
	// Trailing whitespace is itself a word boundary: the next
	// alt+backspace should chew through both the space run and "FROM".
	m.HandleKey("alt+backspace")
	if got := m.Value(); got != "SELECT * " {
		t.Fatalf("alt+backspace should delete ' FROM', got %q", got)
	}
}

func TestCtrlWDeletesWord(t *testing.T) {
	// ctrl+w is the readline fallback for terminals that don't emit
	// alt+backspace as ESC+DEL (or where the user has rebound it).
	m := New()
	m.Focus()
	m.SetValue("one two three")
	m.row, m.col = 0, len("one two three")
	m.HandleKey("ctrl+w")
	if got := m.Value(); got != "one two " {
		t.Fatalf("ctrl+w should delete 'three', got %q", got)
	}
}

func TestAltBackspaceAtLineStartJoinsLine(t *testing.T) {
	// At column 0 the word-delete should degrade to a regular
	// backspace and join with the previous line — anything else
	// silently swallows the keystroke at line boundaries.
	m := New()
	m.Focus()
	m.SetValue("first\nsecond")
	m.row, m.col = 1, 0
	m.HandleKey("alt+backspace")
	if got := m.Value(); got != "firstsecond" {
		t.Fatalf("alt+backspace at col 0 should join lines, got %q", got)
	}
}

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

func TestNoAutoTriggerAfterTable(t *testing.T) {
	// `FROM t ` is a table position — typing a word-rune there must
	// not pop the column list. Used to: that's the user's main bug.
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name"})
	m.Focus()
	m.SetValue("SELECT * FROM t ")
	m.row, m.col = 0, len("SELECT * FROM t ")
	m.HandleKey("w")
	if len(m.suggestions) != 0 {
		t.Fatalf("no suggestions expected after 'FROM t ', got %v", m.suggestions)
	}
}

func TestNoAutoTriggerInSelectWithoutComma(t *testing.T) {
	// `SELECT id ` already has a column; the next word without a
	// comma is not another column.
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name"})
	m.Focus()
	m.SetValue("SELECT id ")
	m.row, m.col = 0, len("SELECT id ")
	m.HandleKey("c")
	if len(m.suggestions) != 0 {
		t.Fatalf("no suggestions expected without preceding comma, got %v", m.suggestions)
	}
}

func TestSuggestionListIsCappedWithHint(t *testing.T) {
	cols := []string{
		"customer_a", "customer_b", "customer_c", "customer_d",
		"customer_e", "customer_f", "customer_g", "customer_h",
		"customer_i", "customer_j", "customer_k",
	}
	m := New()
	m.SetColumns(cols)
	m.Focus()
	m.SetValue("SELECT ")
	m.row, m.col = 0, len("SELECT ")
	m.HandleKey("c")
	if got := len(m.suggestions); got != 8 {
		t.Fatalf("visible cap = 8, got %d", got)
	}
	if m.sugHidden != 3 {
		t.Fatalf("expected 3 hidden, got %d", m.sugHidden)
	}
	if !strings.Contains(m.suggestionsView(), "+3 more") {
		t.Fatalf("view should mention overflow, got %q", m.suggestionsView())
	}
}

func TestOrderByOffersColumnsAlreadyInSelect(t *testing.T) {
	// Regression: the dedup filter must only apply to the SELECT list.
	// In ORDER BY (and GROUP BY), reusing a SELECT column is normal.
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name"})
	m.Focus()
	m.SetValue("SELECT customer_id FROM t ORDER BY ")
	m.row, m.col = 0, len("SELECT customer_id FROM t ORDER BY ")
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("ORDER BY should offer SELECTed columns too, got %v", m.suggestions)
	}
}

func TestAutoTriggerInJoinOn(t *testing.T) {
	m := New()
	m.SetColumns([]string{"customer_id", "customer_name"})
	m.Focus()
	m.SetValue("SELECT * FROM t JOIN x ON ")
	m.row, m.col = 0, len("SELECT * FROM t JOIN x ON ")
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected column list in JOIN ON, got %v", m.suggestions)
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
	m.SetColumns([]string{"customer_id", "customer_name", "order_id", "order_date"})
	m.Focus()
	// Auto-trigger only fires in column positions, so seed a SELECT.
	m.SetValue("SELECT ")
	m.row, m.col = 0, len("SELECT ")
	m.HandleKey("c")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected 2 customer_* entries after 'c', got %v", m.suggestions)
	}
	// Typing that doesn't match → empty list, but completion stays
	// active (backspace recovers).
	m.HandleKey("z")
	if len(m.suggestions) != 0 {
		t.Fatalf("expected empty list after 'cz', got %v", m.suggestions)
	}
	m.HandleKey("backspace")
	if len(m.suggestions) != 2 {
		t.Fatalf("expected recovery to 2 entries on backspace, got %v", m.suggestions)
	}
}

func TestInsertTextPreservesLiteralContent(t *testing.T) {
	// Regression: pasting "FROM t\n" must not auto-complete "t" into
	// the first column starting with that letter.
	m := New()
	m.SetColumns([]string{"typ", "datum"}) // "typ" would match "t"
	m.Focus()
	m.SetValue("")
	m.InsertText("FROM t\nWHERE")
	want := "FROM t\nWHERE"
	if got := m.Value(); got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if len(m.suggestions) != 0 {
		t.Fatal("InsertText must not leave suggestions open")
	}
}

func TestNonWordCharClosesSuggestions(t *testing.T) {
	// Typing a char that isn't part of a word (space, paren, etc.)
	// should dismiss the list. Otherwise Enter after "COUNT(*) "
	// would accept the first column in the dictionary.
	m := New()
	m.SetColumns([]string{"Antal", "Datum"})
	m.Focus()
	m.SetValue("SELECT ")
	m.row, m.col = 0, len("SELECT ")
	m.HandleKey("A") // auto-trigger opens
	if len(m.suggestions) == 0 {
		t.Fatal("expected auto-trigger to open a list")
	}
	m.HandleKey(" ") // non-word char → list should close
	if len(m.suggestions) != 0 {
		t.Fatalf("space should close the list, got %v", m.suggestions)
	}
	// Enter should now insert a newline, not accept a stray suggestion.
	m.HandleKey("enter")
	if got := m.Value(); got != "SELECT A \n" {
		t.Fatalf("want literal newline after space, got %q", got)
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
