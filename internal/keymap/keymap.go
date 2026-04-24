// Package keymap centralizes the app's keybindings so they're easy to
// document and tweak.
package keymap

type Keymap struct {
	RunQuery     []string
	ExportExcel  []string
	FocusEditor  []string
	FocusResults []string
	Quit         []string
	Help         []string
}

// Default returns the app's keybindings.
//
// Some Cmd chords reach the app only on terminals that speak the
// Kitty keyboard protocol (Ghostty, Kitty, WezTerm, modern iTerm2),
// and the rest are intercepted before they arrive:
//   - cmd+t  → terminal "New Tab" (iTerm2 / Terminal.app)
//   - cmd+q  → macOS "Quit Application" (OS-level, never reachable)
//   - cmd+w  → terminal "Close Window"
//
// For focus switching we therefore stick to ctrl+* only. ⌘R and ⌘E
// still work on terminals that forward them (and ctrl+* is always
// available as a fallback).
func Default() Keymap {
	return Keymap{
		RunQuery:     []string{"super+r", "ctrl+r"},
		ExportExcel:  []string{"super+e", "ctrl+e"},
		FocusEditor:  []string{"ctrl+q"},
		FocusResults: []string{"ctrl+t"},
		Quit:         []string{"ctrl+c"},
		Help:         []string{"ctrl+h", "f1"},
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
