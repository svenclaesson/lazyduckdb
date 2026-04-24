// Package table renders a horizontally scrollable result-set view.
//
// The terminal is almost always narrower than a 40-column result, so
// we size each column to its content, render the full grid as a wide
// string, and then clip by a (colOffset, rowOffset) viewport. Arrow
// keys pan around inside that grid.
package table

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const (
	minColWidth  = 3
	maxColWidth  = 40
	colSeparator = " │ "
)

type Model struct {
	columns []string
	rows    [][]string

	colWidths []int

	width     int
	height    int
	rowOffset int
	colOffset int // leftmost visible data column index
	cursorRow int
	focused   bool

	// Client-side "/" search. When searching is true, printable text
	// from HandleText extends searchQuery; Enter advances to the next
	// match; Esc exits. Match positions are kept precomputed so we
	// don't re-scan on every render.
	searching      bool
	searchQuery    string
	searchMatches  []matchPos
	searchMatchIdx int
}

type matchPos struct {
	row, col int
}

func New() Model {
	return Model{}
}

func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *Model) SetData(columns []string, rows [][]string) {
	m.columns = columns
	m.rows = rows
	m.rowOffset = 0
	m.colOffset = 0
	m.cursorRow = 0
	m.recomputeColWidths()
}

func (m *Model) Clear() {
	m.columns = nil
	m.rows = nil
	m.colWidths = nil
	m.rowOffset = 0
	m.colOffset = 0
	m.cursorRow = 0
}

func (m *Model) Focus()           { m.focused = true }
func (m *Model) Blur()            { m.focused = false }
func (m *Model) Focused() bool    { return m.focused }
func (m *Model) IsSearching() bool { return m.searching }

// Scroll{Left,Right,Up,Down} are focus-independent pan operations.
// They're called from the mouse-wheel handler in the top-level model
// so the table responds to wheel events even when the editor has
// keyboard focus. Each call moves by one cell in the requested
// direction and is a no-op at the edge.
func (m *Model) ScrollLeft() {
	if m.colOffset > 0 {
		m.colOffset--
	}
}

func (m *Model) ScrollRight() {
	if len(m.columns) > 0 && m.colOffset < len(m.columns)-1 {
		m.colOffset++
	}
}

func (m *Model) ScrollUp() {
	if m.rowOffset > 0 {
		m.rowOffset--
	}
	if m.cursorRow > 0 && m.cursorRow >= m.rowOffset+m.visibleDataRows() {
		m.cursorRow = m.rowOffset + m.visibleDataRows() - 1
	}
}

func (m *Model) ScrollDown() {
	if len(m.rows) == 0 {
		return
	}
	if m.rowOffset+m.visibleDataRows() < len(m.rows) {
		m.rowOffset++
	}
	if m.cursorRow < m.rowOffset {
		m.cursorRow = m.rowOffset
	}
}

func (m *Model) RowCount() int    { return len(m.rows) }
func (m *Model) ColCount() int    { return len(m.columns) }
func (m *Model) Columns() []string { return m.columns }
func (m *Model) Rows() [][]string  { return m.rows }

func (m *Model) recomputeColWidths() {
	m.colWidths = make([]int, len(m.columns))
	for i, name := range m.columns {
		m.colWidths[i] = clamp(utf8.RuneCountInString(name), minColWidth, maxColWidth)
	}
	for _, row := range m.rows {
		for i, cell := range row {
			if i >= len(m.colWidths) {
				break
			}
			w := utf8.RuneCountInString(cell)
			if w > m.colWidths[i] {
				m.colWidths[i] = clamp(w, minColWidth, maxColWidth)
			}
		}
	}
}

// HandleText is the printable-text entry point (space included). It's
// the only way search mode ingests characters — regular key handling
// can't see the space character in Bubble Tea v2 (msg.String() returns
// "space"). Returns true if the input was consumed (meaning no other
// handling should happen).
func (m *Model) HandleText(s string) bool {
	if !m.focused || s == "" {
		return false
	}
	if !m.searching {
		if s == "/" && len(m.rows) > 0 {
			m.searching = true
			m.searchQuery = ""
			m.searchMatches = nil
			m.searchMatchIdx = -1
			return true
		}
		return false
	}
	// In search mode — append to query (skip a leading "/" so typing
	// "/" to enter search doesn't self-echo into the query).
	m.searchQuery += s
	m.refreshSearch()
	return true
}

// HandleKey processes navigation keys within the results table.
// Returns true if consumed.
func (m *Model) HandleKey(key string) bool {
	if !m.focused {
		return false
	}
	// Search-mode first: these keys only mean something while the
	// "/" prompt is open. Other keys fall through to the normal
	// navigation switch below so the user can still scroll around
	// with highlights active.
	if m.searching {
		if m.handleSearchKey(key) {
			return true
		}
	}
	if len(m.rows) == 0 {
		return false
	}
	switch key {
	case "left":
		if m.colOffset > 0 {
			m.colOffset--
		}
	case "right":
		if m.colOffset < len(m.columns)-1 {
			m.colOffset++
		}
	case "shift+left", "home":
		m.colOffset = 0
	case "shift+right", "end":
		m.colOffset = len(m.columns) - 1
	case "up":
		if m.cursorRow > 0 {
			m.cursorRow--
		}
		if m.cursorRow < m.rowOffset {
			m.rowOffset = m.cursorRow
		}
	case "down":
		if m.cursorRow < len(m.rows)-1 {
			m.cursorRow++
		}
		visible := m.visibleDataRows()
		if m.cursorRow >= m.rowOffset+visible {
			m.rowOffset = m.cursorRow - visible + 1
		}
	case "pgup":
		step := m.visibleDataRows()
		m.cursorRow = max(0, m.cursorRow-step)
		m.rowOffset = max(0, m.rowOffset-step)
	case "pgdown":
		step := m.visibleDataRows()
		m.cursorRow = min(len(m.rows)-1, m.cursorRow+step)
		visible := m.visibleDataRows()
		if m.cursorRow >= m.rowOffset+visible {
			m.rowOffset = m.cursorRow - visible + 1
		}
	default:
		return false
	}
	return true
}

// handleSearchKey drives key input while the / search prompt is open.
// Enter advances to the next match, Esc exits, Backspace trims the
// query. Returns false for any other key so the caller falls through
// to the normal navigation switch — this lets the user scroll with
// arrow keys while the search query and highlights stay active.
func (m *Model) handleSearchKey(key string) bool {
	switch key {
	case "esc":
		m.exitSearch()
		return true
	case "enter":
		m.jumpToNextMatch()
		return true
	case "backspace":
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
			m.refreshSearch()
		}
		return true
	}
	return false
}

// refreshSearch recomputes the match set from the current query and
// jumps to the first match. Called after every query edit.
func (m *Model) refreshSearch() {
	if m.searchQuery == "" {
		m.searchMatches = nil
		m.searchMatchIdx = -1
		return
	}
	q := strings.ToLower(m.searchQuery)
	m.searchMatches = m.searchMatches[:0]
	for r, row := range m.rows {
		for c, cell := range row {
			if strings.Contains(strings.ToLower(cell), q) {
				m.searchMatches = append(m.searchMatches, matchPos{r, c})
			}
		}
	}
	m.searchMatchIdx = -1
	if len(m.searchMatches) > 0 {
		m.jumpToNextMatch()
	}
}

// jumpToNextMatch cycles through the match list in row-major order.
func (m *Model) jumpToNextMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	m.searchMatchIdx = (m.searchMatchIdx + 1) % len(m.searchMatches)
	pos := m.searchMatches[m.searchMatchIdx]
	m.cursorRow = pos.row
	// Ensure row is visible.
	if m.cursorRow < m.rowOffset {
		m.rowOffset = m.cursorRow
	}
	visible := m.visibleDataRows()
	if m.cursorRow >= m.rowOffset+visible {
		m.rowOffset = m.cursorRow - visible + 1
	}
	// Ensure the matched column is visible — simplest policy is to
	// make it the leftmost visible column. Cheap and predictable;
	// the alternative (compute whether it already fits) needs the
	// same availWidth calculation the renderer uses.
	m.colOffset = pos.col
}

func (m *Model) exitSearch() {
	m.searching = false
	m.searchQuery = ""
	m.searchMatches = nil
	m.searchMatchIdx = -1
}

// visibleDataRows is body height excluding header (1) and status (1).
func (m *Model) visibleDataRows() int {
	n := m.height - 3 // title + header + status
	if n < 1 {
		return 1
	}
	return n
}

var (
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	cellStyle    = lipgloss.NewStyle()
	cursorStyle  = lipgloss.NewStyle().Reverse(true)
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	searchStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	matchStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("220"))
	borderColor  = lipgloss.Color("240")
	focusedColor = lipgloss.Color("63")
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
)

func (m Model) View() string {
	title := titleStyle.Render("Results ")
	if len(m.columns) == 0 {
		box := m.box("(run a query to see results)", m.width)
		return title + "\n" + box
	}

	// Account for the box: 2 chars of border + 2 chars of horizontal
	// padding = 4 total that eat into the terminal width before the
	// table content even starts. Sizing content to anything wider
	// makes lipgloss wrap the right border onto a new line — that's
	// the "header jumps to next row" bug.
	availWidth := m.width - 4
	if availWidth < 10 {
		availWidth = 10
	}

	visibleCols, widths := m.pickVisibleColumns(availWidth)
	var b strings.Builder

	// Header row.
	hdrCells := make([]string, len(visibleCols))
	for i, idx := range visibleCols {
		hdrCells[i] = headerStyle.Render(padOrTruncate(m.columns[idx], widths[i]))
	}
	b.WriteString(strings.Join(hdrCells, colSeparator))
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("─", availWidth))
	b.WriteByte('\n')

	// Data rows.
	visibleRows := m.visibleDataRows()
	end := min(len(m.rows), m.rowOffset+visibleRows)
	for r := m.rowOffset; r < end; r++ {
		cells := make([]string, len(visibleCols))
		for i, idx := range visibleCols {
			val := ""
			if idx < len(m.rows[r]) {
				val = m.rows[r][idx]
			}
			text := padOrTruncate(val, widths[i])
			style := cellStyle
			if r == m.cursorRow {
				style = cursorStyle
			}
			rendered := style.Render(text)
			// Overlay search-match highlights on top of the base
			// cell style. Done after the row-style pass so the
			// highlight wins visually even on the cursor row.
			if m.searching && m.searchQuery != "" {
				rendered = highlightMatches(text, m.searchQuery, style)
			}
			cells[i] = rendered
		}
		b.WriteString(strings.Join(cells, colSeparator))
		b.WriteByte('\n')
	}
	// Pad empty rows.
	shown := end - m.rowOffset
	for i := shown; i < visibleRows; i++ {
		b.WriteByte('\n')
	}

	if m.searching {
		line := fmt.Sprintf("/%s", m.searchQuery)
		switch {
		case len(m.searchMatches) == 0 && m.searchQuery != "":
			line += "   (no matches — esc to cancel)"
		case len(m.searchMatches) > 0:
			line += fmt.Sprintf("   (%d/%d — enter for next, esc to cancel)",
				m.searchMatchIdx+1, len(m.searchMatches))
		default:
			line += "   (type to search, enter next, esc cancel)"
		}
		b.WriteString(searchStyle.Render(line))
	} else {
		status := fmt.Sprintf("row %d/%d  col %d-%d/%d  (/ to search)",
			m.cursorRow+1, len(m.rows),
			visibleCols[0]+1, visibleCols[len(visibleCols)-1]+1, len(m.columns),
		)
		b.WriteString(statusStyle.Render(status))
	}

	return title + "\n" + m.box(b.String(), m.width)
}

func (m Model) box(content string, width int) string {
	color := borderColor
	if m.focused {
		color = focusedColor
	}
	// lipgloss v2: Width(n) is the total rendered block width,
	// including border and padding. With 1-char border each side and
	// Padding(0, 1), the interior content area is n-4. We render
	// content at availWidth = m.width - 4, so the box itself must be
	// m.width wide to make the arithmetic match — otherwise the last
	// column overflows and lipgloss wraps the right border down a
	// line (the "header jumps to next row" bug).
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(width).
		Render(content)
}

// pickVisibleColumns greedily fills the available width starting at
// colOffset. It always renders at least one column.
func (m Model) pickVisibleColumns(avail int) ([]int, []int) {
	var cols []int
	var widths []int
	used := 0
	sepLen := utf8.RuneCountInString(colSeparator)
	for i := m.colOffset; i < len(m.columns); i++ {
		w := m.colWidths[i]
		need := w
		if len(cols) > 0 {
			need += sepLen
		}
		if used+need > avail && len(cols) > 0 {
			break
		}
		cols = append(cols, i)
		widths = append(widths, w)
		used += need
	}
	if len(cols) == 0 {
		// Even a single column overflows — truncate it to fit.
		cols = []int{m.colOffset}
		widths = []int{avail}
	}
	return cols, widths
}

// highlightMatches returns the cell text with every case-insensitive
// occurrence of needle wrapped in matchStyle, and the rest wrapped in
// the caller's base style so the resulting string keeps the cursor
// row's reverse-video look on non-matching characters. The input text
// has already been padded with padOrTruncate, so widths stay stable.
func highlightMatches(text, needle string, base lipgloss.Style) string {
	if needle == "" {
		return base.Render(text)
	}
	lowerText := strings.ToLower(text)
	lowerNeedle := strings.ToLower(needle)
	var b strings.Builder
	i := 0
	for i < len(lowerText) {
		idx := strings.Index(lowerText[i:], lowerNeedle)
		if idx < 0 {
			b.WriteString(base.Render(text[i:]))
			break
		}
		if idx > 0 {
			b.WriteString(base.Render(text[i : i+idx]))
		}
		end := i + idx + len(lowerNeedle)
		b.WriteString(matchStyle.Render(text[i+idx : end]))
		i = end
	}
	return b.String()
}

func padOrTruncate(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n == width {
		return s
	}
	if n > width {
		if width <= 1 {
			return string([]rune(s)[:width])
		}
		return string([]rune(s)[:width-1]) + "…"
	}
	return s + strings.Repeat(" ", width-n)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
