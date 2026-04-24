// Package picker is a tiny Bubble Tea program that lets the user
// choose a parquet file from the current directory before the main
// app starts.
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
	m := newModel(files)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	fm := final.(model)
	if fm.aborted {
		return "", nil
	}
	return fm.files[fm.cursor], nil
}

type model struct {
	files   []string
	cursor  int
	aborted bool
	done    bool
}

func newModel(files []string) model {
	return model{files: files}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "esc", "q":
		m.aborted = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.files) - 1
	case "enter":
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
	pickerItemStyle = lipgloss.NewStyle().Padding(0, 1)
)

func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString(pickerTitleStyle.Render(
		fmt.Sprintf("Select a parquet file (%d found)", len(m.files))))
	b.WriteString("\n\n")
	for i, f := range m.files {
		name := filepath.Base(f)
		if i == m.cursor {
			b.WriteString(pickerActiveStyle.Render("▶ " + name))
		} else {
			b.WriteString(pickerItemStyle.Render("  " + name))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(pickerHelpStyle.Render("↑/↓ move · enter select · q/esc cancel"))
	return tea.NewView(b.String())
}
