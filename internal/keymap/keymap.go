// Package keymap centralizes the app's keybindings so they're easy to
// document and tweak.
package keymap

type Keymap struct {
	RunQuery    []string
	ExportExcel []string
	FocusEditor []string
	Quit        []string
	Help        []string
}

// Default returns the app's keybindings. Focus model is
// query-driven: running a query auto-focuses the results pane, and
// Esc returns focus to the editor. There's no "jump to results"
// shortcut — that's deliberate.
//
// Some Cmd chords don't reach terminal apps on macOS:
//   - cmd+t  → terminal "New Tab" (iTerm2 / Terminal.app)
//   - cmd+q  → macOS "Quit Application" (OS-level, never reachable)
//   - cmd+w  → terminal "Close Window"
//
// ⌘R and ⌘E reach the app only on terminals that speak the Kitty
// keyboard protocol (Ghostty, Kitty, WezTerm, modern iTerm2); the
// ctrl+* variants are always available as a fallback.
func Default() Keymap {
	return Keymap{
		RunQuery:    []string{"super+r", "ctrl+r"},
		ExportExcel: []string{"super+e", "ctrl+e"},
		FocusEditor: []string{"ctrl+q"},
		Quit:        []string{"ctrl+c"},
		Help:        []string{"ctrl+h", "f1"},
	}
}

func (k Keymap) Matches(key string, set []string) bool {
	for _, s := range set {
		if s == key {
			return true
		}
	}
	return false
}
