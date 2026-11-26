package uikit

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A code block's Copy action confirms with a check for a moment and keeps its
// accessible name; the frame the timer asks for takes the check away.
func TestCodeBlockCopyShowsCheckThenReturns(t *testing.T) {
	var flash CopyFlash
	clicks := 0
	tt := ui.NewTester(func(c *ui.Context) {
		CodeBlock(c, "go build ./...", func() { clicks++ }, &flash)
	}, 420, 120)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy: %v", err)
	}
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("Copy ran %d times", clicks)
	}
	if flash.at.IsZero() || tt.Now().Sub(flash.at) >= copyFlashFor {
		t.Fatal("the copied check did not show")
	}
	if _, ok := tt.Find("Copy"); !ok {
		t.Fatal("the copy button lost its accessible name")
	}
	tt.Advance(2 * time.Second)
	tt.Frame()
	if tt.Now().Sub(flash.at) < copyFlashFor {
		t.Fatal("the copied check stayed after its moment")
	}
}
