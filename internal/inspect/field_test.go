package inspect

import (
	"context"
	"strings"
	"testing"
)

func TestJSONField(t *testing.T) {
	input := `{"ID":9007199254740993,"id":19,"a/b":{"~key":[false,null,"值"]},"empty":""}`
	for _, tc := range []struct{ pointer, want string }{
		{"/ID", "9007199254740993"}, {"/id", "19"},
		{"/a~1b/~0key/0", "false"}, {"/a~1b/~0key/1", "null"}, {"/a~1b/~0key/2", "值"},
		{"/a~1b/~0key/02", ""}, {"/missing", ""}, {"/empty", ""},
	} {
		got, err := JSONField(t.Context(), input, tc.pointer)
		if err != nil || got != tc.want {
			t.Fatalf("%q = %q, %v; want %q", tc.pointer, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ input, pointer string }{
		{`{"id":1,"id":2}`, "/id"}, {`{"id":1} trailing`, "/id"},
		{input, "/a~1b"}, {input, "$.id"}, {input, "/~2"},
		{strings.Repeat(" ", 1<<20+1), "/id"},
		{`"` + strings.Repeat("x", 4097) + `"`, ""},
	} {
		if value, err := JSONField(t.Context(), tc.input, tc.pointer); err == nil || value != "" {
			t.Fatalf("accepted invalid/unbounded field %q: %q, %v", tc.pointer, value, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := JSONField(ctx, input, "/id"); err == nil {
		t.Fatal("ignored cancellation")
	}
}
