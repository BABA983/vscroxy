package app

import (
	"strings"
	"testing"
)

func TestCaptureDiffMatchesTheViewerHunks(t *testing.T) {
	got := captureDiff("capture-1-response-body.txt", "capture-2-response-body.txt",
		"a\nb\nc\n", "a\nB\nc\nd\n")
	want := "--- a/capture-1-response-body.txt\n+++ b/capture-2-response-body.txt\n" +
		"@@ -1,3 +1,4 @@\n a\n-b\n+B\n c\n+d\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if same := captureDiff("a.txt", "b.txt", "same\n", "same\n"); same != "" {
		t.Fatalf("equal texts produced a diff:\n%s", same)
	}
	// Changes farther apart than the context become two hunks; nearer ones
	// share a hunk with the unchanged lines between them.
	two := captureDiff("a", "b", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n", "1\nX\n3\n4\n5\n6\n7\n8\n9\n10\n11\nY\n13\n14\n")
	if n := strings.Count(two, "@@ -"); n != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", n, two)
	}
	near := captureDiff("a", "b", "1\n2\n3\n4\n5\n", "1\nX\n3\nY\n5\n")
	if n := strings.Count(near, "@@ -"); n != 1 {
		t.Fatalf("want 1 hunk, got %d:\n%s", n, near)
	}
}
