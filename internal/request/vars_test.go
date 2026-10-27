package request

import "testing"

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"base": "http://127.0.0.1:8099", "token": "abc"}
	cases := []struct{ in, want string }{
		{"{{base}}/page.html", "http://127.0.0.1:8099/page.html"},
		{"{{base}}/x?t={{token}}", "http://127.0.0.1:8099/x?t=abc"},
		{"Bearer {{token}}", "Bearer abc"},
		// A name with no value stays as written, so the mistake shows.
		{"{{missing}}/x", "{{missing}}/x"},
		{"no variables here", "no variables here"},
		{"{{base}", "{{base}"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExpandVars(c.in, vars); got != c.want {
			t.Errorf("expandVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := ExpandVars("{{base}}", nil); got != "{{base}}" {
		t.Errorf("with no variables = %q, want it unchanged", got)
	}
}
