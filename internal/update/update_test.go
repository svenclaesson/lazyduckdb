package update

import "testing"

func TestNewerSemver(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "0.1", true},
		{"0.1.1", "0.1", true},
		{"v0.1", "v0.1", false},
		{"v0.1", "0.2.0", false},
		{"v1.0.0", "0.9.9", true},
		{"v0.10.0", "v0.2.0", true}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q,%q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestPromptTextMentionsCommandAndVersions(t *testing.T) {
	got := PromptText("v0.2.0", "0.1")
	for _, want := range []string{"v0.2.0", "v0.1", InstallCommand, "press enter"} {
		if !contains(got, want) {
			t.Errorf("PromptText missing %q in:\n%s", want, got)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
