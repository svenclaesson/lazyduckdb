// Package picker is a tiny Bubble Tea program that lets the user
// choose a parquet file from the current directory before the main
// app starts. The Model type is also embedded inside the main app
// to drive the in-app "attach another file" flow.
package picker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// FindParquetFiles returns every *.parquet file directly inside dir,
// sorted alphabetically.
func FindParquetFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".parquet") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Pick shows an interactive list and returns the user's choice.
// path is a single parquet file when the user picked one normally;
// when the user pressed ctrl+a in search mode to commit every
// matching file as a group, path is a display label (e.g.
// "*test*.parquet") and group is the resolved file list. path is
// "" with both nil/empty when the user aborted.
func Pick(files []string) (path string, group []string, err error) {
	m := New(files)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", nil, err
	}
	fm := final.(Model)
	if fm.aborted {
		return "", nil, nil
	}
	if g := fm.SelectedGlob(); g != "" {
		return g, fm.SelectedFiles(), nil
	}
	if len(fm.visible) == 0 {
		return "", nil, nil
	}
	return fm.files[fm.visible[fm.cursor]], nil, nil
}

// Model is the picker state. Exported so it can be embedded inside
// the main app and driven from there as a sub-state instead of a
// standalone Bubble Tea program.
type Model struct {
	files   []string
	visible []int // indices into files that match query (all when query == "")
	cursor  int   // position within visible
	aborted bool
	done    bool

	searching bool
	query     string

	// chosenGlob, when non-empty, means the user pressed ctrl+a to
	// commit the current search as a group. The string is a display
	// label (e.g. "*test*.parquet") — the actual file set is in
	// chosenFiles, snapshotted from the visible list at commit time.
	// Display-only because the picker filter is case-insensitive
	// while filesystem globs are case-sensitive on Linux/macOS-CS;
	// passing the resolved paths avoids that mismatch entirely.
	chosenGlob  string
	chosenFiles []string

	// height is the row budget the picker should fit its rendered
	// body into. Standalone runs pick this up from WindowSizeMsg;
	// the embedded usage in app.go calls SetSize explicitly with
	// the leftover height after the surrounding chrome. Zero means
	// "render every visible item" — the legacy fallback before the
	// viewport was added.
	height int
}

// SetSize tells the picker how many rows it has to render its body
// into. Used by the embedded caller (the main app) to forward the
// terminal-size budget; the standalone Run() picks it up itself
// from WindowSizeMsg.
func (m *Model) SetSize(_ int, height int) { m.height = height }

// New constructs a picker for the given files. The list starts
// unfiltered; call Update with key events to drive it.
func New(files []string) Model {
	m := Model{files: files}
	return m.refilter()
}

// Done reports whether the user has confirmed a selection.
func (m Model) Done() bool { return m.done }

// Aborted reports whether the user dismissed the picker.
func (m Model) Aborted() bool { return m.aborted }

// Selected returns the path the cursor currently points at, or "" if
// the visible list is empty. When the user committed a glob via
// ctrl+a in search mode, the glob pattern is returned instead of a
// concrete file path. Stable to call before Done().
func (m Model) Selected() string {
	if m.chosenGlob != "" {
		return m.chosenGlob
	}
	if len(m.visible) == 0 {
		return ""
	}
	return m.files[m.visible[m.cursor]]
}

// SelectedGlob returns the glob pattern the user committed via
// ctrl+a, or "" when a single file was picked normally. Embedded
// callers (the main app's attach flow) use this to distinguish
// "open these N files as a group" from "open this one file".
func (m Model) SelectedGlob() string { return m.chosenGlob }

// SelectedFiles returns the resolved file list captured when the
// user pressed ctrl+a, in the same order they were visible. Empty
// for single-file picks. Always prefer this over the glob pattern
// when actually mounting the source — it bypasses filesystem
// case-sensitivity quirks (the picker filter is case-insensitive).
func (m Model) SelectedFiles() []string { return m.chosenFiles }

func (m Model) Init() tea.Cmd { return nil }

// queryToGlob translates the picker's substring search query into a
// shell glob that matches the same files the substring filter just
// showed on screen. The mapping has to honor what the user sees:
// "test" filters to anything containing "test" — so the glob is
// *test*.parquet, not test*.parquet.
func queryToGlob(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "*.parquet"
	}
	// User typed a literal glob — trust them, just ensure the .parquet suffix.
	if strings.ContainsAny(q, "*?[") {
		if strings.HasSuffix(strings.ToLower(q), ".parquet") {
			return q
		}
		return q + ".parquet"
	}
	return "*" + q + "*.parquet"
}

// refilter recomputes visible from files and the current query, then
// clamps the cursor.
func (m Model) refilter() Model {
	m.visible = m.visible[:0]
	if m.query == "" {
		for i := range m.files {
			m.visible = append(m.visible, i)
		}
	} else {
		q := strings.ToLower(m.query)
		for i, f := range m.files {
			if strings.Contains(strings.ToLower(filepath.Base(f)), q) {
				m.visible = append(m.visible, i)
			}
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = 0
	}
	return m
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		// Standalone-Run path: Bubble Tea forwards the terminal size
		// here. Embedded usage doesn't go through this — the app
		// calls SetSize directly via propagateSize() instead.
		m.height = size.Height
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	s := key.String()

	if s == "ctrl+c" {
		m.aborted = true
		return m, tea.Quit
	}

	if m.searching {
		switch s {
		case "esc":
			m.searching = false
			m.query = ""
			return m.refilter(), nil
		case "ctrl+a":
			// Commit every currently-visible file as a group.
			// chosenGlob is just the display label; chosenFiles is
			// the authoritative list, snapshotted from the visible
			// state so case-insensitive filter matches survive a
			// case-sensitive filesystem.
			if len(m.visible) == 0 {
				return m, nil
			}
			m.chosenGlob = queryToGlob(m.query)
			m.chosenFiles = make([]string, len(m.visible))
			for i, idx := range m.visible {
				m.chosenFiles[i] = m.files[idx]
			}
			m.done = true
			return m, tea.Quit
		case "enter":
			if len(m.visible) == 0 {
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		case "backspace":
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
				return m.refilter(), nil
			}
			return m, nil
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
			return m, nil
		case "home":
			m.cursor = 0
			return m, nil
		case "end":
			if len(m.visible) > 0 {
				m.cursor = len(m.visible) - 1
			}
			return m, nil
		}
		if key.Text != "" {
			m.query += key.Text
			return m.refilter(), nil
		}
		return m, nil
	}

	switch s {
	case "esc", "q":
		m.aborted = true
		return m, tea.Quit
	case "/":
		m.searching = true
		m.query = ""
		return m.refilter(), nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		if len(m.visible) > 0 {
			m.cursor = len(m.visible) - 1
		}
	case "enter":
		if len(m.visible) == 0 {
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

var (
	pickerTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).Padding(0, 1)
	pickerHelpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	pickerActiveStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("63")).
				Foreground(lipgloss.Color("0")).
				Padding(0, 1)
	pickerItemStyle  = lipgloss.NewStyle().Padding(0, 1)
	pickerQueryStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220")).Padding(0, 1)
)

// ViewBody renders the picker as a string. The standalone View
// wraps this in a tea.View; embedded callers (the main app) splice
// the body straight into their own composite view.
func (m Model) ViewBody() string {
	var b strings.Builder
	title := fmt.Sprintf("Select a parquet file (%d found)", len(m.files))
	if m.searching || m.query != "" {
		title = fmt.Sprintf("Select a parquet file (%d of %d match)", len(m.visible), len(m.files))
	}
	b.WriteString(pickerTitleStyle.Render(title))
	b.WriteString("\n\n")

	if len(m.visible) == 0 {
		b.WriteString(pickerItemStyle.Render("(no matches)"))
		b.WriteByte('\n')
	}
	start, end := m.itemWindow()
	if start > 0 {
		b.WriteString(pickerHelpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		name := filepath.Base(m.files[m.visible[i]])
		if i == m.cursor {
			b.WriteString(pickerActiveStyle.Render("▶ " + name))
		} else {
			b.WriteString(pickerItemStyle.Render("  " + name))
		}
		b.WriteByte('\n')
	}
	if end < len(m.visible) {
		b.WriteString(pickerHelpStyle.Render(fmt.Sprintf("  ↓ %d more below", len(m.visible)-end)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if m.searching {
		b.WriteString(pickerQueryStyle.Render("/" + m.query))
		b.WriteByte('\n')
		b.WriteString(pickerHelpStyle.Render("type to filter · ↑/↓ move · enter select · ctrl+a group all matches · esc clear · ctrl+c quit"))
	} else {
		b.WriteString(pickerHelpStyle.Render("↑/↓ move · / search · enter select · q/esc cancel"))
	}
	return b.String()
}

func (m Model) View() tea.View {
	return tea.NewView(m.ViewBody())
}

// itemWindow returns the [start, end) slice of m.visible that
// should actually be rendered, sized to fit m.height and scrolled
// so the cursor is always inside the window. When height is 0 or
// the list fits entirely, returns the full range — preserving the
// pre-viewport behavior so small dirs render unchanged.
//
// Chrome budget subtracted from m.height:
//   title (1) + blank (1) + ↑more (1) + ↓more (1) +
//   blank (1) + query (1, search mode) + help (1) = ~7
// We deduct 7 so the window leaves room for both scroll markers
// even when only one is needed; the small underuse is worth not
// having the layout jitter as the cursor crosses the boundary.
func (m Model) itemWindow() (start, end int) {
	n := len(m.visible)
	if n == 0 {
		return 0, 0
	}
	const chrome = 7
	rows := m.height - chrome
	if rows <= 0 || rows >= n {
		return 0, n
	}
	// Anchor the window so the cursor is always visible. We center
	// when there's slack on both sides; clamp to the head/tail
	// when near the ends.
	start = m.cursor - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > n {
		start = n - rows
	}
	return start, start + rows
}
