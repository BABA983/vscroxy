package unicodechars

import (
	"slices"
	"testing"
)

// Cases in TestEscapeControlAndFormatCharacters, TestContainsAmbiguousCharacter,
// TestContainsInvisibleCharacter and TestIsBasicASCII are ported from VS Code's
// src/vs/base/test/common/strings.test.ts at the generated data's commit.
func TestEscapeControlAndFormatCharacters(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`C:\Users\me\My Project`, `C:\Users\me\My Project`},
		{"/work/api\n\nWorking directory: /work/web", `/work/api\u{a}\u{a}Working directory: /work/web`},
		{"root-\u202ea-toor\u200b\t\u2028end\U000e0001", `root-\u{202e}a-toor\u{200b}\u{9}\u{2028}end\u{e0001}`},
	}
	for _, test := range tests {
		if got := EscapeControlAndFormatCharacters(test.input); got != test.want {
			t.Errorf("EscapeControlAndFormatCharacters(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestContainsAmbiguousCharacter(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abcd", false},
		{"üå", false},
		{"(*&^)", false},
		{"ο", true},
		{"abɡc", true},
	}
	instance := GetInstance(nil)
	for _, test := range tests {
		if got := instance.ContainsAmbiguousCharacter(test.input); got != test.want {
			t.Errorf("ContainsAmbiguousCharacter(%q) = %t, want %t", test.input, got, test.want)
		}
	}
}

func TestContainsInvisibleCharacter(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abcd", false},
		{" ", true},
		{"a\U000e004eb", true},
		{"a\U000e015a\u000bb", true},
	}
	for _, test := range tests {
		if got := (InvisibleCharacters{}).ContainsInvisibleCharacter(test.input); got != test.want {
			t.Errorf("ContainsInvisibleCharacter(%q) = %t, want %t", test.input, got, test.want)
		}
	}
}

func TestIsBasicASCII(t *testing.T) {
	punctuation := "`~!@#$%^&*()-_=+[{]}\\|;:'\",<.>/?"
	all := "abcdefghijklmnopqrstuvwxyz" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"1234567890" + punctuation + " \t\n\r"
	tests := []struct {
		input string
		want  bool
	}{
		{"abcdefghijklmnopqrstuvwxyz", true},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", true},
		{"1234567890", true},
		{punctuation, true},
		{" ", true},
		{"\t", true},
		{"\n", true},
		{"\r", true},
		{all, true},
		{"\u001f", false},
		{"\u007f", false},
		{"ü", false},
		{"a📚📚b", false},
	}
	for _, test := range tests {
		if got := IsBasicASCII(test.input); got != test.want {
			t.Errorf("IsBasicASCII(%q) = %t, want %t", test.input, got, test.want)
		}
	}
}

func TestInvisibleCodePoints(t *testing.T) {
	points := (InvisibleCharacters{}).CodePoints()
	if !slices.IsSorted(points) {
		t.Fatal("CodePoints is not sorted")
	}
	for _, codePoint := range []rune{'\u200b', '\u00a0', '\U000e0001'} {
		if !(InvisibleCharacters{}).IsInvisibleCharacter(codePoint) {
			t.Errorf("U+%04X is not in the invisible set", codePoint)
		}
		if !slices.Contains(points, codePoint) {
			t.Errorf("U+%04X is missing from CodePoints", codePoint)
		}
	}
	if (InvisibleCharacters{}).IsInvisibleCharacter('a') {
		t.Error("'a' is in the invisible set")
	}
}

func TestAmbiguousCharactersLocales(t *testing.T) {
	if got, ok := GetInstance(nil).GetPrimaryConfusable('а'); !ok || got != 'a' {
		t.Errorf("default primary confusable for Cyrillic а = %q, %t; want 'a', true", got, ok)
	}

	// In VS Code's data, Russian considers Cyrillic а ordinary, while zh-hans
	// keeps it confusable with Latin a.
	if GetInstance([]string{"ru"}).IsAmbiguous('а') {
		t.Error("ru locale flags Cyrillic а as ambiguous")
	}
	if !GetInstance([]string{"zh-hans"}).IsAmbiguous('а') {
		t.Error("zh-hans locale does not flag Cyrillic а as ambiguous")
	}
	if got, ok := GetInstance([]string{"zh-hans"}).GetPrimaryConfusable('а'); !ok || got != 'a' {
		t.Errorf("zh-hans primary confusable for Cyrillic а = %q, %t; want 'a', true", got, ok)
	}

	if !slices.Contains(GetLocales(), "zh-hans") || slices.Contains(GetLocales(), "_default") {
		t.Errorf("unexpected locales: %v", GetLocales())
	}
}
