package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/editor"
)

func newEditorForTest() editor.Model {
	return editor.New()
}

// Build a model with a synthetic result set loaded, focus on results.
// We skip duck.Open (which needs CGo + a real parquet) and just pre-
// populate the table model directly — the test only exercises the
// Bubble Tea keyboard wiring.
func buildAppWithResults(t *testing.T) Model {
	t.Helper()
	m := Model{
		session: &duck.Session{},
	}
	m.editor = m.editor // keep zero value — focus starts false
	m.results.SetSize(200, 20)
	rows := make([][]string, 0, 10)
	for i := 0; i < 10; i++ {
		rows = append(rows, []string{"r"})
	}
	m.results.SetData([]string{"c"}, rows)
	m.results.Focus()
	m.focus = focusResults
	m.width = 200
	m.height = 20
	return m
}

func TestDownKeyAdvancesResultsCursor(t *testing.T) {
	m := buildAppWithResults(t)

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)

	if got := m.results.CursorRow(); got != 1 {
		t.Fatalf("after one down: want cursorRow=1, got %d", got)
	}
}

func TestEscFromEditorTogglesToResults(t *testing.T) {
	// Issue #1: esc in the editor should flip focus to the results
	// pane, mirroring the existing results→editor direction. The
	// toggle is gated on having a result set loaded — without one
	// there's nothing to scroll, so esc should be a no-op.
	m := Model{
		session: &duck.Session{},
		editor:  newEditorForTest(),
		focus:   focusEditor,
		width:   200,
		height:  20,
	}
	m.editor.Focus()
	m.results.SetSize(200, 20)
	m.results.SetData([]string{"c"}, [][]string{{"x"}})
	m.lastResult = &duck.ResultSet{
		Columns:   []string{"c"},
		Rows:      [][]string{{"x"}},
		TotalRows: 1,
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.focus != focusResults {
		t.Fatalf("esc in editor should switch focus to results, got %v", m.focus)
	}
	if !m.results.Focused() {
		t.Fatal("results pane should be focused after esc from editor")
	}
}

func TestEscFromEditorWithoutResultsIsNoOp(t *testing.T) {
	// Without a loaded result set, there's nothing to scroll, so
	// esc must not steal focus away from the editor.
	m := Model{
		session: &duck.Session{},
		editor:  newEditorForTest(),
		focus:   focusEditor,
		width:   200,
		height:  20,
	}
	m.editor.Focus()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.focus != focusEditor {
		t.Fatalf("esc with no results should keep focus on editor, got %v", m.focus)
	}
}

func TestQueryResultFocusesResults(t *testing.T) {
	m := Model{
		session: &duck.Session{},
		editor:  newEditorForTest(),
		focus:   focusEditor,
		width:   200,
		height:  20,
	}
	m.editor.Focus()

	// Synthesize a successful query result arrival.
	rs := &duck.ResultSet{
		Columns:   []string{"c"},
		Rows:      [][]string{{"x"}, {"y"}},
		TotalRows: 2,
	}
	next, _ := m.Update(queryResultMsg{rs: rs})
	m = next.(Model)

	if m.focus != focusResults {
		t.Fatalf("focus should switch to results after query, got %v", m.focus)
	}
	if !m.results.Focused() {
		t.Fatal("results pane should be focused")
	}

	// Now down should advance the cursor on the results pane.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if got := m.results.CursorRow(); got != 1 {
		t.Fatalf("after down on fresh results: want cursorRow=1, got %d", got)
	}
}
