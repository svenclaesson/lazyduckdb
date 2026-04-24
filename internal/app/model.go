// Package app is the top-level Bubble Tea model. It owns the DuckDB
// session, the query editor, the results table, and a status/help bar.
package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/editor"
	"github.com/svenclaesson/lazyduckdb/internal/export"
	"github.com/svenclaesson/lazyduckdb/internal/keymap"
	"github.com/svenclaesson/lazyduckdb/internal/table"
)

type focus int

const (
	focusEditor focus = iota
	focusResults
)

type Model struct {
	session *duck.Session
	keymap  keymap.Keymap

	editor  editor.Model
	results table.Model

	focus  focus
	status string

	width  int
	height int
}

func NewModel(session *duck.Session) Model {
	ed := editor.New()
	ed.SetDictionary(buildDictionary(session.ColumnNames()))
	ed.SetColumns(session.ColumnNames())
	ed.SetValue("SELECT * FROM t")

	rt := table.New()

	km := keymap.Default()

	return Model{
		session: session,
		keymap:  km,
		editor:  ed,
		results: rt,
		focus:   focusEditor,
		status: fmt.Sprintf("loaded %d columns from %s — ⌘R run (shows first %d rows), ⌘E export all",
			len(session.Columns), session.ParquetPath, displayLimit),
	}
}

func (m Model) Init() tea.Cmd { return nil }

// buildDictionary merges schema column names with a small SQL keyword
// set. Column names always come first so prefix matches prefer them
// over generic keywords.
func buildDictionary(columns []string) []string {
	keywords := []string{
		"SELECT", "FROM", "WHERE", "GROUP BY", "ORDER BY", "LIMIT", "OFFSET",
		"AS", "AND", "OR", "NOT", "IN", "IS", "NULL", "LIKE", "BETWEEN",
		"JOIN", "LEFT JOIN", "RIGHT JOIN", "INNER JOIN", "OUTER JOIN",
		"ON", "UNION", "DISTINCT", "HAVING", "CASE", "WHEN", "THEN", "ELSE", "END",
		"COUNT(*)", "SUM", "AVG", "MIN", "MAX",
	}
	out := append([]string{}, columns...)
	return append(out, keywords...)
}

var (
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Padding(0, 1)
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Padding(0, 1)
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63")).
			Padding(0, 1)
)

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("starting...")
		v.AltScreen = true
		return v
	}
	// Size is persisted via propagateSize() when WindowSizeMsg
	// arrives; the values the children use during HandleKey are
	// therefore in sync with what's rendered here.

	head := headerStyle.Render(
		fmt.Sprintf("lazyduckdb  •  %s  •  %d columns",
			shortPath(m.session.ParquetPath), len(m.session.Columns)))

	style := statusStyle
	if strings.HasPrefix(m.status, "error:") {
		style = errorStyle
	}

	content := strings.Join([]string{
		head,
		m.editor.View(),
		m.results.View(),
		style.Render(m.status),
		statusStyle.Render(m.footer()),
	}, "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	// Mouse reporting is intentionally OFF so the terminal's native
	// click-and-drag text selection / copy works. Enabling cell-motion
	// mouse mode (for scroll-wheel table scrolling) would steal the
	// click event and break copy-paste. Keyboard navigation covers
	// horizontal + vertical scroll (←/→, Home/End, ↑/↓, PgUp/PgDn).
	return v
}

func (m Model) footer() string {
	pane := "editor"
	if m.focus == focusResults {
		pane = "results"
	}
	return fmt.Sprintf("[%s] ⌘R run (→ results)  ⌘E excel  esc → editor  ctrl+c quit", pane)
}

func shortPath(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// --- Update ---

type queryResultMsg struct {
	rs  *duck.ResultSet
	err error
}

type exportResultMsg struct {
	path string
	note string
	err  error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Propagate the new terminal size down to child components
		// *here* (not in View — View has a value receiver, so any
		// SetSize call there is thrown away). Without this, the
		// table's internal height stays 0 and visibleDataRows() gets
		// clamped to 1, turning every ↓ press into a viewport scroll.
		m.propagateSize()
		return m, nil

	case queryResultMsg:
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			return m, nil
		}
		m.results.SetData(msg.rs.Columns, msg.rs.Rows)
		m.status = formatResultStatus(msg.rs)
		// Auto-focus the results pane after a successful run so the
		// user can scroll, search, and navigate immediately. Esc
		// returns focus to the editor (handled in handleKey below).
		m.setFocus(focusResults)
		return m, nil

	case exportResultMsg:
		switch {
		case msg.err != nil && msg.path != "":
			m.status = "exported with warning → " + msg.path + " (" + msg.err.Error() + ")"
		case msg.err != nil:
			m.status = "error: " + msg.err.Error()
		case msg.note != "":
			// Successful export with an informational note (e.g. sheet split).
			m.status = "exported → " + msg.path + " — " + msg.note
		default:
			m.status = "exported → " + msg.path
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseWheelMsg:
		return m.handleWheel(msg)

	case tea.PasteMsg:
		// Bracketed-paste. Terminals send the pasted text as a single
		// message, separate from KeyPressMsg — without this handler
		// ⌘V / Ctrl+Shift+V silently drops the clipboard contents.
		return m.handlePaste(msg), nil
	}
	return m, nil
}

func (m Model) handlePaste(msg tea.PasteMsg) tea.Model {
	text := msg.Content
	if text == "" {
		return m
	}
	switch m.focus {
	case focusEditor:
		// Feed each rune through the editor so multi-line paste
		// respects newlines (each one hits insertNewline) and all
		// autocomplete bookkeeping stays consistent.
		for _, r := range text {
			if r == '\n' {
				m.editor.HandleKey("enter")
				continue
			}
			m.editor.HandleKey(string(r))
		}
	case focusResults:
		// In the results pane, paste only makes sense while the /
		// search prompt is open — append to the query.
		if m.results.IsSearching() {
			for _, r := range text {
				if r == '\n' || r == '\r' {
					continue
				}
				m.results.HandleText(string(r))
			}
		}
	}
	return m
}

// handleWheel pans the results table in response to the scroll wheel.
// Horizontal wheel events (two-finger swipe on macOS trackpads) come
// through as MouseWheelLeft/Right directly. Vertical wheel + Shift is
// also mapped to horizontal for users whose terminals don't surface
// the horizontal wheel — this is the same convention most IDEs use.
func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	shift := mouse.Mod&tea.ModShift != 0
	switch mouse.Button {
	case tea.MouseWheelLeft:
		m.results.ScrollLeft()
	case tea.MouseWheelRight:
		m.results.ScrollRight()
	case tea.MouseWheelUp:
		if shift {
			m.results.ScrollLeft()
		} else {
			m.results.ScrollUp()
		}
	case tea.MouseWheelDown:
		if shift {
			m.results.ScrollRight()
		} else {
			m.results.ScrollDown()
		}
	}
	return m, nil
}

// propagateSize pushes the current terminal size into the child
// components. Must be called on every WindowSizeMsg so HandleKey
// (which runs on Update's stored model, not View's transient copy)
// sees the correct height when computing visibleDataRows.
func (m *Model) propagateSize() {
	editorHeight := 6
	statusHeight := 2
	resultsHeight := m.height - editorHeight - statusHeight - 4
	if resultsHeight < 1 {
		resultsHeight = 1
	}
	editorInner := editorHeight - 2
	if editorInner < 1 {
		editorInner = 1
	}
	m.editor.SetSize(m.width, editorInner)
	m.results.SetSize(m.width, resultsHeight)
}

func (m *Model) setFocus(f focus) {
	m.focus = f
	if f == focusEditor {
		m.editor.Focus()
		m.results.Blur()
	} else {
		m.editor.Blur()
		m.results.Focus()
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch {
	case m.keymap.Matches(key, m.keymap.Quit):
		return m, tea.Quit
	case m.keymap.Matches(key, m.keymap.RunQuery):
		return m, m.runQueryCmd()
	case m.keymap.Matches(key, m.keymap.ExportExcel):
		return m, m.exportCmd()
	case m.keymap.Matches(key, m.keymap.FocusEditor):
		m.setFocus(focusEditor)
		return m, nil
	case key == "esc" && m.focus == focusResults && !m.results.IsSearching():
		// If a / search is open, esc should exit the search first —
		// only bounce focus back to the editor when no search is
		// active. The table's own esc handler runs via HandleKey
		// further down.
		m.setFocus(focusEditor)
		return m, nil
	}

	// Route to the focused pane. For both panes we prefer the raw
	// printable text (msg.Text) over msg.String() — Bubble Tea v2
	// returns "space" for the spacebar, not " ", and neither the
	// editor's rune-insert path nor the results' / search prompt
	// would see spaces otherwise. For multi-rune Text (IME
	// composition) we forward each rune separately.
	if m.focus == focusEditor {
		if msg.Text != "" {
			for _, r := range msg.Text {
				m.editor.HandleKey(string(r))
			}
		} else {
			m.editor.HandleKey(key)
		}
	} else {
		if msg.Text != "" {
			for _, r := range msg.Text {
				if m.results.HandleText(string(r)) {
					continue
				}
			}
		}
		m.results.HandleKey(key)
	}
	return m, nil
}

// displayLimit caps the number of rows we materialize for on-screen
// display. The TotalRows field on the result set still reports the
// true count so the user knows what's hidden. Excel export re-runs
// the query with no cap so the exported sheet is complete.
const displayLimit = 100

func (m Model) runQueryCmd() tea.Cmd {
	sql := strings.TrimSpace(m.editor.Value())
	if sql == "" {
		return func() tea.Msg {
			return queryResultMsg{err: fmt.Errorf("empty query")}
		}
	}
	return func() tea.Msg {
		rs, err := m.session.Query(sql, displayLimit)
		return queryResultMsg{rs: rs, err: err}
	}
}

// formatResultStatus produces the line shown beneath the results
// table. It's a function so it can be unit-tested without spinning
// up the whole Bubble Tea model.
func formatResultStatus(rs *duck.ResultSet) string {
	shown := len(rs.Rows)
	switch {
	case rs.TotalRows < 0:
		return fmt.Sprintf("%d rows shown × %d cols — total unknown (non-SELECT?)",
			shown, len(rs.Columns))
	case rs.TotalRows <= shown:
		return fmt.Sprintf("%d rows × %d cols — ← →/PgUp/PgDn to scroll, esc→editor, ⌘E exports all",
			shown, len(rs.Columns))
	default:
		return fmt.Sprintf("showing %d of %d rows × %d cols — ← →/PgUp/PgDn to scroll, esc→editor, ⌘E exports all %d",
			shown, rs.TotalRows, len(rs.Columns), rs.TotalRows)
	}
}

func (m Model) exportCmd() tea.Cmd {
	sql := strings.TrimSpace(m.editor.Value())
	if sql == "" {
		return func() tea.Msg {
			return exportResultMsg{err: fmt.Errorf("no query to export")}
		}
	}
	// Re-run the query without a row cap so the exported Excel sheet
	// contains the full result set, not just the 100 rows on screen.
	return func() tea.Msg {
		rs, err := m.session.Query(sql, 0)
		if err != nil {
			return exportResultMsg{err: err}
		}
		path, note, err := export.ToExcel(rs.Columns, rs.Rows)
		return exportResultMsg{path: path, note: note, err: err}
	}
}
