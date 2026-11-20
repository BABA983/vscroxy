package version

import (
	"encoding/json/v2"
	"os"
	"strings"
	"testing"
)

// TestNumberMatchesMyGoJSON keeps the number the binaries print equal to the
// one the packages carry.
func TestNumberMatchesMyGoJSON(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Version != Number {
		t.Fatalf("mygo.json version %q, version.Number %q", config.Version, Number)
	}
}

func TestStringCarriesTheCommitWhenKnown(t *testing.T) {
	old := Commit
	defer func() { Commit = old }()
	Commit = "abc1234"
	if got := String(); got != Number+" (abc1234)" {
		t.Fatalf("String() = %q", got)
	}
	Commit = ""
	if got := String(); !strings.HasPrefix(got, Number) {
		t.Fatalf("String() = %q", got)
	}
	if Platform() == "" || !strings.Contains(Platform(), "/") {
		t.Fatalf("Platform() = %q", Platform())
	}
}

func TestStringShowsOnlyADifferentBuild(t *testing.T) {
	old := Commit
	t.Cleanup(func() { Commit = old })
	for _, tc := range []struct {
		name  string
		build string
		want  string
	}{
		{"build equals version", Number, Number},
		{"commit", "abc1234", Number + " (abc1234)"},
		{"build number", "123", Number + " (123)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Commit = tc.build
			if got := String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
