package natural

import (
	"slices"
	"testing"
)

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		one, other string
		want       int
	}{
		{"req2", "req10", -1},
		{"req10", "req2", 1},
		{"a-2b", "a-10b", -1},
		{"file2part3", "file2part12", -1},
		{"A3", "a3", 0},
		{"a02", "a2", 0},
		{"a000", "a0", 0},
		{"no digits", "No digits", 0},
		{"alpha", "beta", -1},
		{"a", "a2", -1},
		{"req", "req0", -1},
	} {
		if got := sign(Compare(tc.one, tc.other)); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.one, tc.other, got, tc.want)
		}
		if got := sign(Compare(tc.other, tc.one)); got != -tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.other, tc.one, got, -tc.want)
		}
	}
}

func TestStableSortKeepsTies(t *testing.T) {
	names := []string{"a10", "no-digits", "A3", "a2", "a02", "Beta", "beta"}
	slices.SortStableFunc(names, Compare)
	want := []string{"a2", "a02", "A3", "a10", "Beta", "beta", "no-digits"}
	if !slices.Equal(names, want) {
		t.Fatalf("sorted = %q, want %q", names, want)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
