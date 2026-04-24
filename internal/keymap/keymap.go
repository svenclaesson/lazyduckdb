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

// Default returns macOS-native bindings. Cmd keys arrive as "super+*"
// when the terminal supports the Kitty keyboard protocol (Ghostty,
// Kitty, WezTerm, modern iTerm2). On terminals that don't, the Cmd
// press is either swallowed by the OS or never reaches the app, so we
// keep a "ctrl+*" fallback for those users (and because ctrl+c is the
// universal Quit contract in a TUI).
func Default() Keymap {
	return Keymap{
		RunQuery:     []string{"super+r", "ctrl+r"},
		ExportExcel:  []string{"super+e", "ctrl+e"},
		FocusEditor:  []string{"super+q", "ctrl+q"},
		FocusResults: []string{"super+t", "ctrl+t"},
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
