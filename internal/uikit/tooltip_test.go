package uikit

import (
	"strings"
	"testing"
)

// Tooltip shortcuts are quiet joined text, not a row of Kbd caps.
func TestTooltipShortcutLabel(t *testing.T) {
	for _, tc := range []struct {
		keys []string
		want string
	}{
		{[]string{"⌘", "B"}, "⌘B"},
		{[]string{"Ctrl", "S"}, "Ctrl S"},
		{[]string{"⌘", "⇧", "Z"}, "⌘⇧Z"},
	} {
		if got := shortcutLabel(tc.keys); got != tc.want {
			t.Errorf("shortcutLabel(%v) = %q, want %q", tc.keys, got, tc.want)
		}
	}
}

// A long URL in a tip gets places to wrap; a sentence keeps its words.
func TestTipBreaksALongURL(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("segment", 12)
	broken := breakLong(url)
	if !strings.Contains(broken, "/\u200b") {
		t.Fatalf("a long URL did not break after its slash: %q", broken)
	}
	if strings.Contains(broken, "segmentseg\u200b") {
		t.Fatalf("a path with a slash still broke inside a word: %q", broken)
	}
	if got := breakLong("short"); got != "short" {
		t.Fatalf("short text changed to %q", got)
	}
}
