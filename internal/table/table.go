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

	width      int
	height     int
	rowOffset  int
	colOffset  int // leftmost visible data column index
	cursorRow  int
	focused    bool
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

func (m *Model) Focus()        { m.focused = true }
func (m *Model) Blur()         { m.focused = false }
func (m *Model) Focused() bool { return m.focused }

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

// HandleKey processes navigation keys within the results table.
// Returns true if consumed.
func (m *Model) HandleKey(key string) bool {
	if !m.focused || len(m.rows) == 0 {
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
			cells[i] = style.Render(text)
		}
		b.WriteString(strings.Join(cells, colSeparator))
		b.WriteByte('\n')
	}
	// Pad empty rows.
	shown := end - m.rowOffset
	for i := shown; i < visibleRows; i++ {
		b.WriteByte('\n')
	}

	status := fmt.Sprintf("row %d/%d  col %d-%d/%d",
		m.cursorRow+1, len(m.rows),
		visibleCols[0]+1, visibleCols[len(visibleCols)-1]+1, len(m.columns),
	)
	b.WriteString(statusStyle.Render(status))

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
