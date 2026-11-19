package request

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func scriptTests(t *testing.T, src string) []TestResult {
	t.Helper()
	run := &scriptRun{vars: map[string]string{}}
	if err := runScript(src, run, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	return run.tests
}

func TestAssertionDiff(t *testing.T) {
	tests := scriptTests(t, `pm.test("deep equal", function () { pm.expect({b: [2], a: 1}).to.deep.equal({a: 1, b: [3]}) });
		pm.test("equal", function () { pm.expect(1).to.equal(2) });
		pm.test("ok", function () { pm.expect(0).to.be.ok });
		pm.test("passes", function () { pm.expect(1).to.equal(1) });`)
	if len(tests) != 4 {
		t.Fatalf("tests = %+v, want four", tests)
	}
	wantExpected := "{\n  \"a\": 1,\n  \"b\": [\n    3\n  ]\n}"
	wantActual := "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}"
	if !tests[0].HasDiff || tests[0].Expected != wantExpected || tests[0].Actual != wantActual {
		t.Errorf("deep equal diff = %q/%q, hasDiff %v; want %q/%q",
			tests[0].Expected, tests[0].Actual, tests[0].HasDiff, wantExpected, wantActual)
	}
	if !tests[1].HasDiff || tests[1].Expected != "2" || tests[1].Actual != "1" {
		t.Errorf("equal diff = %q/%q, hasDiff %v; want 2/1", tests[1].Expected, tests[1].Actual, tests[1].HasDiff)
	}
	if tests[2].HasDiff || tests[2].Expected != "" || tests[2].Actual != "" {
		t.Errorf("message-only test has a diff: %+v", tests[2])
	}
	if tests[3].HasDiff {
		t.Errorf("passing test has a diff: %+v", tests[3])
	}
}

func TestAssertionDiffTruncatesEachSide(t *testing.T) {
	marker := "\n... [truncated]"
	limit := 64 * 1024
	tests := scriptTests(t, `pm.test("long", function () { pm.expect("`+strings.Repeat("e", 70000)+`").to.equal("`+strings.Repeat("a", 70000)+`") });`)
	if len(tests) != 1 || !tests[0].HasDiff {
		t.Fatalf("tests = %+v, want one diff", tests)
	}
	want := strings.Repeat("a", limit-len(marker)) + marker
	if tests[0].Expected != want {
		t.Errorf("expected length = %d, suffix %q, want length %d and truncation marker", len(tests[0].Expected), tests[0].Expected[len(tests[0].Expected)-len(marker):], len(want))
	}
	want = strings.Repeat("e", limit-len(marker)) + marker
	if tests[0].Actual != want {
		t.Errorf("actual length = %d, suffix %q, want length %d and truncation marker", len(tests[0].Actual), tests[0].Actual[len(tests[0].Actual)-len(marker):], len(want))
	}
}

func TestAssertionDiffBoundsUTF8Bytes(t *testing.T) {
	tests := scriptTests(t, `pm.test("wide", function () { pm.expect("`+strings.Repeat("世", 40000)+`").to.equal("x") });`)
	if len(tests) != 1 || !tests[0].HasDiff {
		t.Fatalf("tests = %+v, want one diff", tests)
	}
	for _, side := range []string{tests[0].Expected, tests[0].Actual} {
		if len(side) > 64*1024 || !utf8.ValidString(side) {
			t.Errorf("side is %d bytes, valid UTF-8 %v; want at most 64 KiB and valid", len(side), utf8.ValidString(side))
		}
	}
}
