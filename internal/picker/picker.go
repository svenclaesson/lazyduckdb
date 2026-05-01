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

// Pick shows an interactive list and returns the selected path.
// Returns "" with a nil error if the user aborted.
func Pick(files []string) (string, error) {
	m := New(files)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	fm := final.(Model)
	if fm.aborted || len(fm.visible) == 0 {
		return "", nil
	}
	return fm.files[fm.visible[fm.cursor]], nil
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
}

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
// the visible list is empty. Stable to call before Done().
func (m Model) Selected() string {
	if len(m.visible) == 0 {
		return ""
	}
	return m.files[m.visible[m.cursor]]
}

func (m Model) Init() tea.Cmd { return nil }

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
	for i, idx := range m.visible {
		name := filepath.Base(m.files[idx])
		if i == m.cursor {
			b.WriteString(pickerActiveStyle.Render("▶ " + name))
		} else {
			b.WriteString(pickerItemStyle.Render("  " + name))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if m.searching {
		b.WriteString(pickerQueryStyle.Render("/" + m.query))
		b.WriteByte('\n')
		b.WriteString(pickerHelpStyle.Render("type to filter · ↑/↓ move · enter select · esc clear · ctrl+c quit"))
	} else {
		b.WriteString(pickerHelpStyle.Render("↑/↓ move · / search · enter select · q/esc cancel"))
	}
	return b.String()
}

func (m Model) View() tea.View {
	return tea.NewView(m.ViewBody())
}
