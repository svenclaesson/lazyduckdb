// Package editor is a minimal multi-line SQL editor with tab-completion
// over a caller-supplied dictionary (column names + SQL keywords).
package editor

import (
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// Model is the editor state. It renders as a boxed multi-line input
// with an inline suggestions line underneath when completions are active.
type Model struct {
	lines       []string
	row, col    int
	focused     bool
	width       int
	height      int
	dictionary  []string // sorted, used for manual Tab
	columns     []string // sorted, used for auto-trigger in SELECT..FROM
	suggestions []string
	sugSource   []string // which list the live list was drawn from (for refilter)
	sugIdx      int
}

func New() Model {
	return Model{
		lines:   []string{""},
		focused: true,
	}
}

func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetDictionary replaces the combined completion dictionary used by
// manual Tab (columns + SQL keywords merged).
func (m *Model) SetDictionary(words []string) {
	m.dictionary = sortedCopy(words)
}

// SetColumns is the columns-only list used for auto-trigger inside
// the SELECT..FROM column region. Keep this separate from the full
// dictionary so auto-trigger doesn't suggest "SELECT" when the user
// types "s" inside the column list.
func (m *Model) SetColumns(cols []string) {
	m.columns = sortedCopy(cols)
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func (m *Model) Focus()          { m.focused = true }
func (m *Model) Blur()            { m.focused = false; m.clearSuggestions() }
func (m *Model) Focused() bool   { return m.focused }

// Value returns the full editor text with \n joiners.
func (m *Model) Value() string {
	return strings.Join(m.lines, "\n")
}

// SetValue replaces the text and moves caret to end.
func (m *Model) SetValue(s string) {
	m.lines = strings.Split(s, "\n")
	if len(m.lines) == 0 {
		m.lines = []string{""}
	}
	m.row = len(m.lines) - 1
	m.col = len([]rune(m.lines[m.row]))
	m.clearSuggestions()
}

// HandleKey processes a single key. It returns true if the key was
// consumed by the editor. The caller is expected to translate terminal
// key events into the simple strings used here (see app.Update).
func (m *Model) HandleKey(key string) bool {
	if !m.focused {
		return false
	}
	switch key {
	case "tab":
		m.handleTab()
		return true
	case "esc":
		if len(m.suggestions) > 0 {
			m.clearSuggestions()
			return true
		}
		return false
	case "enter":
		// Enter accepts the highlighted suggestion when the list is
		// open, otherwise it's a normal newline. This matches shell
		// and IDE completion UX — the user picks with ↑/↓ and confirms
		// with Enter.
		if len(m.suggestions) > 0 {
			m.acceptSuggestion()
			return true
		}
		m.insertNewline()
		return true
	case "backspace":
		// Use sugSource (not suggestions length) as the "completion
		// is active" signal — the visible list may have emptied out
		// after typing a non-matching char, but we still want
		// backspace to re-widen against the same source.
		inCompletion := m.sugSource != nil
		m.backspace()
		if inCompletion {
			m.refilterSuggestions()
		}
		return true
	case "left":
		if len(m.suggestions) > 0 {
			m.sugIdx = (m.sugIdx - 1 + len(m.suggestions)) % len(m.suggestions)
			return true
		}
		m.moveLeft()
		return true
	case "right":
		if len(m.suggestions) > 0 {
			m.sugIdx = (m.sugIdx + 1) % len(m.suggestions)
			return true
		}
		m.moveRight()
		return true
	// macOS Terminal and iTerm2 default to sending Option+Left as "ESC b"
	// (and Option+Right as "ESC f"), matching readline. Users who've
	// enabled "Option as Meta" or a CSI-u profile get the "alt+left"
	// form instead. Handle both so word navigation works out of the box.
	case "alt+left", "alt+b":
		m.clearSuggestions()
		m.moveWordLeft()
		return true
	case "alt+right", "alt+f":
		m.clearSuggestions()
		m.moveWordRight()
		return true
	case "up":
		m.clearSuggestions()
		m.moveUp()
		return true
	case "down":
		m.clearSuggestions()
		m.moveDown()
		return true
	case "home":
		m.clearSuggestions()
		m.col = 0
		return true
	case "end":
		m.clearSuggestions()
		m.col = len([]rune(m.lines[m.row]))
		return true
	}
	if len(key) == 1 {
		r := rune(key[0])
		inCompletion := m.sugSource != nil
		m.insertRune(r)
		switch {
		case inCompletion:
			// Narrow the list live as the user types more of the
			// prefix. Stays active even if the current match set is
			// empty so a subsequent backspace can widen it back.
			m.refilterSuggestions()
		case isWordRune(r):
			// Auto-open the column list for any word-rune keystroke.
			// Columns are useful in SELECT, WHERE, GROUP BY, ORDER BY,
			// JOIN...ON — basically everywhere in a query — so gating
			// on "between SELECT and FROM" was too restrictive. Press
			// Esc to dismiss if you don't want it.
			m.openColumnSuggestions()
		}
		return true
	}
	return false
}

func (m *Model) insertRune(r rune) {
	line := []rune(m.lines[m.row])
	line = append(line[:m.col], append([]rune{r}, line[m.col:]...)...)
	m.lines[m.row] = string(line)
	m.col++
}

func (m *Model) insertNewline() {
	line := []rune(m.lines[m.row])
	left := string(line[:m.col])
	right := string(line[m.col:])
	m.lines[m.row] = left
	rest := append([]string{right}, m.lines[m.row+1:]...)
	m.lines = append(m.lines[:m.row+1], rest...)
	m.row++
	m.col = 0
}

func (m *Model) backspace() {
	if m.col > 0 {
		line := []rune(m.lines[m.row])
		line = append(line[:m.col-1], line[m.col:]...)
		m.lines[m.row] = string(line)
		m.col--
		return
	}
	if m.row == 0 {
		return
	}
	prev := []rune(m.lines[m.row-1])
	m.col = len(prev)
	m.lines[m.row-1] = string(prev) + m.lines[m.row]
	m.lines = append(m.lines[:m.row], m.lines[m.row+1:]...)
	m.row--
}

func (m *Model) moveLeft() {
	if m.col > 0 {
		m.col--
		return
	}
	if m.row > 0 {
		m.row--
		m.col = len([]rune(m.lines[m.row]))
	}
}

func (m *Model) moveRight() {
	if m.col < len([]rune(m.lines[m.row])) {
		m.col++
		return
	}
	if m.row < len(m.lines)-1 {
		m.row++
		m.col = 0
	}
}

func (m *Model) moveUp() {
	if m.row == 0 {
		return
	}
	m.row--
	if n := len([]rune(m.lines[m.row])); m.col > n {
		m.col = n
	}
}

// moveWordLeft jumps to the start of the previous word. If already at
// the start of the line, it wraps to the end of the previous line.
// "Word" here means a run of word-runes (letter/digit/_), matching the
// macOS Terminal convention for Option+Left.
func (m *Model) moveWordLeft() {
	if m.col == 0 {
		if m.row > 0 {
			m.row--
			m.col = len([]rune(m.lines[m.row]))
		}
		return
	}
	line := []rune(m.lines[m.row])
	i := m.col
	// Skip trailing non-word chars.
	for i > 0 && !isWordRune(line[i-1]) {
		i--
	}
	// Skip the word itself.
	for i > 0 && isWordRune(line[i-1]) {
		i--
	}
	m.col = i
}

// moveWordRight jumps to the start of the next word. If already at the
// end of the line, it wraps to the start of the next line.
func (m *Model) moveWordRight() {
	line := []rune(m.lines[m.row])
	if m.col >= len(line) {
		if m.row < len(m.lines)-1 {
			m.row++
			m.col = 0
		}
		return
	}
	i := m.col
	// Skip current word.
	for i < len(line) && isWordRune(line[i]) {
		i++
	}
	// Skip separators to land at the start of the next word.
	for i < len(line) && !isWordRune(line[i]) {
		i++
	}
	m.col = i
}

func (m *Model) moveDown() {
	if m.row >= len(m.lines)-1 {
		return
	}
	m.row++
	if n := len([]rune(m.lines[m.row])); m.col > n {
		m.col = n
	}
}

// currentWord returns the word ending at the caret, plus its start
// column in the current line. Used both to decide what to complete
// against and to know what to replace when the user accepts a suggestion.
func (m *Model) currentWord() (word string, startCol int) {
	line := []rune(m.lines[m.row])
	i := m.col
	for i > 0 && isWordRune(line[i-1]) {
		i--
	}
	return string(line[i:m.col]), i
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func (m *Model) handleTab() {
	// Tab is always autocomplete. When the list is already open it
	// accepts the currently selected entry — same as Enter — rather
	// than cycling. Left/Right is the only way to change selection.
	if len(m.suggestions) > 0 {
		m.acceptSuggestion()
		return
	}
	// No word at the caret (e.g. between "SELECT" and "FROM") → show
	// the full dictionary so the user can browse available entries
	// without typing anything first.
	word, _ := m.currentWord()
	m.sugSource = m.dictionary
	m.suggestions = m.removeAlreadyUsed(matchPrefix(m.dictionary, word))
	m.sugIdx = 0
	if len(m.suggestions) == 1 {
		m.acceptSuggestion()
	}
}

// openColumnSuggestions is called by the auto-trigger path (typing a
// word-rune inside the SELECT..FROM column list). It matches against
// the columns-only list so keyword names don't clutter the popup.
func (m *Model) openColumnSuggestions() {
	if len(m.columns) == 0 {
		return
	}
	word, _ := m.currentWord()
	if word == "" {
		return
	}
	m.sugSource = m.columns
	m.suggestions = m.removeAlreadyUsed(matchPrefix(m.columns, word))
	m.sugIdx = 0
}

func (m *Model) acceptSuggestion() {
	if len(m.suggestions) == 0 {
		return
	}
	chosen := m.suggestions[m.sugIdx]
	_, start := m.currentWord()
	line := []rune(m.lines[m.row])
	newLine := append([]rune{}, line[:start]...)
	newLine = append(newLine, []rune(chosen)...)
	newLine = append(newLine, line[m.col:]...)
	m.lines[m.row] = string(newLine)
	m.col = start + len([]rune(chosen))
	m.clearSuggestions()
}

func (m *Model) clearSuggestions() {
	m.suggestions = nil
	m.sugSource = nil
	m.sugIdx = 0
}

// firstWordIndex returns the byte offset of the first word-bounded
// occurrence of needle at or after `from`, or -1.
func firstWordIndex(haystack, needle string, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle && isWordBoundary(haystack, i, i+len(needle)) {
			return i
		}
	}
	return -1
}

func isWordBoundary(s string, start, end int) bool {
	leftOK := start == 0 || !isWordByte(s[start-1])
	rightOK := end >= len(s) || !isWordByte(s[end])
	return leftOK && rightOK
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}

// refilterSuggestions re-runs the prefix match against the current
// word at the caret. Uses whichever source list the active popup was
// drawn from (columns for auto-trigger, full dict for manual Tab) so
// the list's character doesn't change mid-typing.
func (m *Model) refilterSuggestions() {
	word, _ := m.currentWord()
	source := m.sugSource
	if source == nil {
		source = m.dictionary
	}
	m.suggestions = m.removeAlreadyUsed(matchPrefix(source, word))
	if m.sugIdx >= len(m.suggestions) {
		m.sugIdx = 0
	}
}

// removeAlreadyUsed drops suggestions that would duplicate a column
// already present in the SELECT list — i.e. we only filter when the
// caret sits between "SELECT" and "FROM". Everywhere else (WHERE,
// GROUP BY, ORDER BY, JOIN...ON, ...) reusing a column name is
// normal SQL and shouldn't be hidden. Filtering is case-insensitive.
func (m *Model) removeAlreadyUsed(suggestions []string) []string {
	if len(suggestions) == 0 || !m.inSelectList() {
		return suggestions
	}
	text := strings.ToLower(strings.Join(m.lines, "\n"))
	out := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		if firstWordIndex(text, strings.ToLower(s), 0) < 0 {
			out = append(out, s)
		}
	}
	return out
}

// inSelectList reports whether the caret sits between the most recent
// "SELECT" keyword and its matching "FROM". Used to gate the
// already-used filter so it only applies in the SELECT column list.
func (m *Model) inSelectList() bool {
	text := strings.Join(m.lines, "\n")
	offset := 0
	for i := 0; i < m.row; i++ {
		offset += len(m.lines[i]) + 1
	}
	offset += m.col

	lower := strings.ToLower(text)
	selectIdx := lastWordIndex(lower, "select", offset)
	if selectIdx < 0 {
		return false
	}
	after := selectIdx + len("select")
	if offset < after {
		return false
	}
	fromIdx := firstWordIndex(lower, "from", after)
	if fromIdx < 0 {
		return true // no FROM yet — still in the column list
	}
	return offset <= fromIdx
}

// lastWordIndex returns the byte offset of the last word-bounded
// occurrence of needle in haystack[:upto], or -1 if none.
func lastWordIndex(haystack, needle string, upto int) int {
	if upto > len(haystack) {
		upto = len(haystack)
	}
	for i := upto - len(needle); i >= 0; i-- {
		if haystack[i:i+len(needle)] == needle && isWordBoundary(haystack, i, i+len(needle)) {
			return i
		}
	}
	return -1
}

func matchPrefix(dict []string, prefix string) []string {
	lower := strings.ToLower(prefix)
	var out []string
	for _, w := range dict {
		if strings.HasPrefix(strings.ToLower(w), lower) && !strings.EqualFold(w, prefix) {
			out = append(out, w)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

var (
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	borderBlur = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	caretStyle      = lipgloss.NewStyle().Reverse(true)
	sugStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	sugActiveStyle  = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("0"))
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
)

func (m Model) View() string {
	var b strings.Builder
	for i, line := range m.lines {
		rendered := line
		if m.focused && i == m.row {
			r := []rune(line)
			switch {
			case m.col >= len(r):
				rendered = string(r) + caretStyle.Render(" ")
			default:
				rendered = string(r[:m.col]) + caretStyle.Render(string(r[m.col])) + string(r[m.col+1:])
			}
		}
		b.WriteString(rendered)
		if i < len(m.lines)-1 {
			b.WriteByte('\n')
		}
	}
	// Pad to requested height so the surrounding layout stays stable.
	if m.height > 0 {
		have := len(m.lines)
		for have < m.height {
			b.WriteByte('\n')
			have++
		}
	}
	style := borderBlur
	if m.focused {
		style = borderStyle
	}
	// lipgloss v2: Width(n) is the *total* rendered width (border +
	// padding + content). Setting it to m.width makes the box fill
	// the terminal exactly, so the right border doesn't wrap.
	innerWidth := m.width
	if innerWidth < 5 {
		innerWidth = 5
	}
	box := style.Width(innerWidth).Render(b.String())

	header := titleStyle.Render("Query ")
	footer := m.suggestionsView()
	if footer != "" {
		return header + "\n" + box + "\n" + footer
	}
	return header + "\n" + box
}

func (m Model) suggestionsView() string {
	if len(m.suggestions) == 0 {
		return ""
	}
	// Render every cell with the same padded shape — only the style
	// (background) differs for the selected one. If we padded only
	// the selected item its neighbors would visibly shift by 2
	// columns each time the user moved the highlight with ←/→.
	parts := make([]string, len(m.suggestions))
	for i, s := range m.suggestions {
		cell := " " + s + " "
		if i == m.sugIdx {
			parts[i] = sugActiveStyle.Render(cell)
		} else {
			parts[i] = sugStyle.Render(cell)
		}
	}
	return sugStyle.Render("↹ ←/→ ⏎") + strings.Join(parts, " ")
}
