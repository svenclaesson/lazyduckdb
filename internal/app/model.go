// Package app is the top-level Bubble Tea model. It owns the DuckDB
// session, the query editor, the results table, and a status/help bar.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/editor"
	"github.com/svenclaesson/lazyduckdb/internal/export"
	"github.com/svenclaesson/lazyduckdb/internal/keymap"
	"github.com/svenclaesson/lazyduckdb/internal/picker"
	"github.com/svenclaesson/lazyduckdb/internal/table"
)

// modKey is the platform-appropriate label for the primary modifier
// shown in help text. macOS reaches for Cmd (⌘); Windows and Linux
// users see "Ctrl+" instead. The actual bindings always include both
// super+ and ctrl+ forms (see keymap.Default), so the chord works
// everywhere — this only affects the displayed hint.
func modKey() string {
	if runtime.GOOS == "darwin" {
		return "⌘"
	}
	return "Ctrl+"
}

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
	// lastResult retains counts/columns from the most recent successful
	// query so the status line can be re-rendered as focus changes
	// between editor (show run commands) and results (show scroll hints).
	lastResult *duck.ResultSet

	// queryGen / exportGen tag every in-flight async command so a slow
	// run can't clobber a faster follow-up. Each new submit bumps its
	// counter; stale messages whose gen doesn't match are discarded.
	queryGen  int
	exportGen int

	// attacher, when non-nil, takes over the editor/results area and
	// shows a parquet picker. The user selects a file to attach as
	// the next tN view; cancelling drops the picker.
	attacher *picker.Model

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
		status: fmt.Sprintf("loaded %d columns from %s",
			len(session.Columns), session.ParquetPath),
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
	seen := make(map[string]struct{}, len(columns)+len(keywords))
	out := make([]string, 0, len(columns)+len(keywords))
	for _, c := range columns {
		k := strings.ToLower(c)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}
	for _, kw := range keywords {
		k := strings.ToLower(kw)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, kw)
	}
	return out
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
	aliasNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("220"))
	aliasFileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
	aliasRowStyle = lipgloss.NewStyle().Padding(0, 1)
)

func (m Model) headerLine() string {
	return "lazyduckdb"
}

// sourcesLine renders the attached parquets as a tN-aliased row above
// the query editor so the user can always see which view points at
// which file. Cols counts come from each source's own DESCRIBE.
func (m Model) sourcesLine() string {
	parts := make([]string, len(m.session.Sources))
	for i, src := range m.session.Sources {
		parts[i] = aliasNameStyle.Render(src.View) +
			" " + aliasFileStyle.Render(fmt.Sprintf("%s (%d cols)",
			filepath.Base(src.Path), len(src.Columns)))
	}
	return aliasRowStyle.Render(strings.Join(parts, "   "))
}

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("starting...")
		v.AltScreen = true
		return v
	}
	// Size is persisted via propagateSize() when WindowSizeMsg
	// arrives; the values the children use during HandleKey are
	// therefore in sync with what's rendered here.

	head := headerStyle.Render(m.headerLine())

	status := m.statusLine()
	style := statusStyle
	if strings.HasPrefix(status, "error:") {
		style = errorStyle
	}

	var parts []string
	if m.attacher != nil {
		// Attach mode: replace the editor/results panes with the
		// picker so it has room to render the file list.
		parts = []string{head, m.sourcesLine(), m.attacher.ViewBody()}
	} else {
		parts = []string{head, m.sourcesLine(), m.editor.View(), m.results.View()}
	}
	parts = append(parts, style.Render(status), statusStyle.Render(m.footer()))
	content := strings.Join(parts, "\n")

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
	return fmt.Sprintf("[%s] %sR run (→ results)  %sE excel  %sO attach  esc → editor  ctrl+c quit",
		pane, modKey(), modKey(), modKey())
}

// --- Update ---

type queryResultMsg struct {
	gen int
	rs  *duck.ResultSet
	err error
}

type exportResultMsg struct {
	gen  int
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
		// Drop stale results from a prior submit that's been
		// superseded — otherwise a slow query landing after a fast
		// follow-up would clobber the displayed table.
		if msg.gen != m.queryGen {
			return m, nil
		}
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			return m, nil
		}
		m.results.SetData(msg.rs.Columns, msg.rs.Rows)
		m.lastResult = msg.rs
		m.status = ""
		// Auto-focus the results pane after a successful run so the
		// user can scroll, search, and navigate immediately. Esc
		// returns focus to the editor (handled in handleKey below).
		m.setFocus(focusResults)
		return m, nil

	case exportResultMsg:
		if msg.gen != m.exportGen {
			return m, nil
		}
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
		// Use the paste-specific insertion path so autocomplete
		// doesn't fire mid-paste. Otherwise a pasted lone "t" right
		// before a newline would accept the first column starting
		// with "t" because the \n routes through Enter.
		m.editor.InsertText(text)
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
//
// Vertical budget audited from the actual render:
//
//	header(1) + sources(1) + "Query"(1) + editor box(6=4+2) +
//	"Results"(1) + status(1) + footer(1) + 2 results-box borders = 14
//
// Whatever's left we hand to the results table as its m.height.
func (m *Model) propagateSize() {
	const fixedRows = 14
	editorHeight := 6
	editorInner := editorHeight - 2
	resultsHeight := m.height - fixedRows
	if resultsHeight < 1 {
		resultsHeight = 1
	}
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

	// Picker takes the screen when active. Ctrl+C still quits; every
	// other key routes into the picker until it reports done/aborted.
	if m.attacher != nil {
		if m.keymap.Matches(key, m.keymap.Quit) {
			return m, tea.Quit
		}
		return m.handleAttachKey(msg), nil
	}

	switch {
	case m.keymap.Matches(key, m.keymap.Quit):
		return m, tea.Quit
	case m.keymap.Matches(key, m.keymap.RunQuery):
		m.queryGen++
		return m, m.runQueryCmd(m.queryGen)
	case m.keymap.Matches(key, m.keymap.ExportExcel):
		m.exportGen++
		return m, m.exportCmd(m.exportGen)
	case m.keymap.Matches(key, m.keymap.OpenFile):
		return m.openAttacher(), nil
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
		// Same dispatch rule as the editor branch above: printable text
		// (msg.Text non-empty) routes to HandleText so the search prompt
		// can ingest spaces and other printables; everything else
		// (arrows, esc, enter, backspace, ...) routes to HandleKey.
		// Doing both unconditionally would risk double-processing once
		// HandleKey grows a printable case.
		if msg.Text != "" {
			for _, r := range msg.Text {
				m.results.HandleText(string(r))
			}
		} else {
			m.results.HandleKey(key)
		}
	}
	return m, nil
}

// openAttacher scans the current working directory for parquet
// files and opens the picker. Files already attached to the session
// are filtered out so the user can't pick the same file twice. If
// nothing is left to attach, a status-line error is shown instead.
func (m Model) openAttacher() Model {
	cwd, err := os.Getwd()
	if err != nil {
		m.status = "error: " + err.Error()
		return m
	}
	files, err := picker.FindParquetFiles(cwd)
	if err != nil {
		m.status = "error: " + err.Error()
		return m
	}
	already := make(map[string]struct{}, len(m.session.Sources))
	for _, src := range m.session.Sources {
		already[src.Path] = struct{}{}
	}
	available := files[:0:len(files)]
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			abs = f
		}
		if _, dup := already[abs]; !dup {
			available = append(available, f)
		}
	}
	if len(available) == 0 {
		m.status = fmt.Sprintf("error: no other parquet files in %s", cwd)
		return m
	}
	p := picker.New(available)
	m.attacher = &p
	m.status = ""
	return m
}

// handleAttachKey forwards the key to the embedded picker, then
// inspects its post-update state. A done picker triggers the attach
// + dictionary refresh; an aborted picker just clears the overlay.
// We discard the picker's tea.Cmd because its internal flow returns
// tea.Quit on done/aborted — not what we want when embedded.
func (m Model) handleAttachKey(msg tea.KeyPressMsg) Model {
	upd, _ := m.attacher.Update(msg)
	p := upd.(picker.Model)
	m.attacher = &p

	switch {
	case p.Aborted():
		m.attacher = nil
	case p.Done():
		path := p.Selected()
		m.attacher = nil
		if path == "" {
			return m
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		view, err := m.session.Attach(path)
		if err != nil {
			m.status = "error: " + err.Error()
			return m
		}
		// Refresh the editor's autocomplete dictionary against the new
		// union of columns. Existing query text is left alone.
		m.editor.SetDictionary(buildDictionary(m.session.ColumnNames()))
		m.editor.SetColumns(m.session.ColumnNames())
		m.status = fmt.Sprintf("attached %s as %s", filepath.Base(path), view)
	}
	return m
}

// displayLimit caps the number of rows we materialize for on-screen
// display. The TotalRows field on the result set still reports the
// true count so the user knows what's hidden. Excel export re-runs
// the query with no cap so the exported sheet is complete.
const displayLimit = 1000

func (m Model) runQueryCmd(gen int) tea.Cmd {
	sql := strings.TrimSpace(m.editor.Value())
	if sql == "" {
		return func() tea.Msg {
			return queryResultMsg{gen: gen, err: fmt.Errorf("empty query")}
		}
	}
	return func() tea.Msg {
		rs, err := m.session.Query(sql, displayLimit)
		return queryResultMsg{gen: gen, rs: rs, err: err}
	}
}

// statusLine picks what to show beneath the results table. Explicit
// transient messages (errors, export confirmations, the initial
// "loaded N columns" greeting) take priority. Otherwise we re-render
// the last query result, switching the trailing hint based on which
// pane is focused — scroll hints only make sense in the results pane.
func (m Model) statusLine() string {
	if m.status != "" {
		return m.status
	}
	if m.lastResult == nil {
		return ""
	}
	return formatResultStatus(m.lastResult, m.focus)
}

// formatResultStatus produces the line shown beneath the results
// table. It's a function so it can be unit-tested without spinning
// up the whole Bubble Tea model.
func formatResultStatus(rs *duck.ResultSet, f focus) string {
	shown := len(rs.Rows)
	cols := len(rs.Columns)
	hint := editorHint(rs)
	if f == focusResults {
		hint = resultsHint(rs)
	}
	if rs.TotalRows < 0 {
		return fmt.Sprintf("%d rows shown × %d cols — total unknown (non-SELECT?) — %s",
			shown, cols, hint)
	}
	// Always show shown / total so the user can see the result size
	// even when the display fits within the cap (shown == total).
	return fmt.Sprintf("%d / %d rows × %d cols — %s", shown, rs.TotalRows, cols, hint)
}

func editorHint(rs *duck.ResultSet) string {
	if rs.TotalRows > 0 {
		return fmt.Sprintf("%sR re-run · %sE export all %d", modKey(), modKey(), rs.TotalRows)
	}
	return fmt.Sprintf("%sR re-run · %sE export", modKey(), modKey())
}

func resultsHint(rs *duck.ResultSet) string {
	if rs.TotalRows > 0 {
		return fmt.Sprintf("← →/PgUp/PgDn to scroll, esc→editor, %sE exports all %d", modKey(), rs.TotalRows)
	}
	return fmt.Sprintf("← →/PgUp/PgDn to scroll, esc→editor, %sE exports all", modKey())
}

func (m Model) exportCmd(gen int) tea.Cmd {
	sql := strings.TrimSpace(m.editor.Value())
	if sql == "" {
		return func() tea.Msg {
			return exportResultMsg{gen: gen, err: fmt.Errorf("no query to export")}
		}
	}
	// Re-run the query without a row cap so the exported Excel sheet
	// contains the full result set, not just the displayLimit rows on screen.
	return func() tea.Msg {
		rs, err := m.session.Query(sql, 0)
		if err != nil {
			return exportResultMsg{gen: gen, err: err}
		}
		path, note, err := export.ToExcel(rs.Columns, rs.Rows)
		return exportResultMsg{gen: gen, path: path, note: note, err: err}
	}
}
