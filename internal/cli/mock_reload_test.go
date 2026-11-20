package cli_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/mockserver"
	"github.com/baba983/vscroxy/internal/request"
)

func readMockRequest(t *testing.T, path string) request.Request {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r request.Request
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// One mock serves the collection through edits, additions and deletions of
// its examples. Every change is observed through a reload line, never a
// sleep; a file that cannot be parsed keeps the last working set answering.
func TestMockReloadsExamplesWithoutRestart(t *testing.T) {
	root := mockCollection(t)
	path := filepath.Join(root, "Shop", "Items.request.json")
	base, stdout, stderr, stop := startMock(t, root)
	defer stop()

	if code, body := mockGet(t, "GET", base+"/items/42"); code != 200 || body != `{"from":"first"}` {
		t.Fatalf("initial answer: %d %q", code, body)
	}

	edited := readMockRequest(t, path)
	edited.Examples[0].Body = `{"from":"edited"}`
	edited.Examples = append(edited.Examples, request.Example{ID: "added", Name: "added", Status: 200,
		Request: request.ExampleRequest{Method: "GET", URL: "http://api.example.test/added"}, Body: `{"from":"added"}`})
	write(t, path, edited)
	waitMockOutput(t, stdout, "Reloaded 3 examples")
	if code, body := mockGet(t, "GET", base+"/items/42"); code != 200 || body != `{"from":"edited"}` {
		t.Errorf("edited answer: %d %q", code, body)
	}
	if code, body := mockGet(t, "GET", base+"/added"); code != 200 || body != `{"from":"added"}` {
		t.Errorf("added answer: %d %q", code, body)
	}

	deleted := readMockRequest(t, path)
	deleted.Examples = deleted.Examples[:2]
	write(t, path, deleted)
	waitMockOutput(t, stdout, "Reloaded 2 examples")
	if code, body := mockGet(t, "GET", base+"/added"); code != 404 {
		t.Errorf("deleted route: %d %q, want 404", code, body)
	}

	if err := os.WriteFile(path, []byte(`{"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	waitMockLine(t, stderr, "keeping the last working examples", func() string {
		return fmt.Sprintf("broken file was not reported: stderr=%q", stderr.String())
	})
	if code, body := mockGet(t, "GET", base+"/items/42"); code != 200 || body != `{"from":"edited"}` {
		t.Errorf("answer after a broken file: %d %q, want the last good set", code, body)
	}

	fixed := edited
	fixed.Examples = fixed.Examples[:1]
	write(t, path, fixed)
	waitMockOutput(t, stdout, "Reloaded 1 examples")
	if code, body := mockGet(t, "GET", base+"/items/42"); code != 200 || body != `{"from":"edited"}` {
		t.Errorf("answer after repair: %d %q", code, body)
	}
	if code, _ := mockGet(t, "GET", base+"/items/42", mockserver.SelectHeader, "gone"); code != 200 {
		t.Errorf("deleted example was still selectable: %d", code)
	}
}

func mockRunCollection(t *testing.T, delayMS int) string {
	t.Helper()
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "Shop", "Pet.request.json"), request.Request{
		ID: "pet", Name: "Pet", Collection: "Shop", Method: "GET", URL: "{{base}}/v1/pets/7",
		Examples: []request.Example{{ID: "mocked", Name: "mocked", Status: 200,
			Headers: "Content-Type: application/json", Body: `{"name":"only-the-mock"}`,
			Request: request.ExampleRequest{Method: "GET", URL: "http://api.test/v1/pets/7"}, DelayMS: delayMS}},
		Checks: request.Checks{Status: "200", JSON: `/name = "only-the-mock"`},
	})
	return input
}

// run --mock answers from the collection's examples: the checks here can only
// pass against the mock, not the empty base variable an ordinary run sees.
func TestRunWithMockAnswersCollectionChecks(t *testing.T) {
	input := mockRunCollection(t, 0)
	report := filepath.Join(t.TempDir(), "run.json")
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"run", "--mock", input, "--report-json", report, input}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "1 completed · 0 failed") || !strings.Contains(out.String(), "Mock for this run at http://127.0.0.1:") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

// Cancellation and the whole-run deadline both end the run with the mock's
// watcher and listeners joined; the package's leak check verifies the join.
func TestRunMockJoinsOnCancelAndTimeout(t *testing.T) {
	input := mockRunCollection(t, 400)
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var out syncText
		errOut := &bytes.Buffer{}
		exit := make(chan int, 1)
		go func() { exit <- cli.Run(ctx, []string{"run", "--mock", input, input}, &out, errOut) }()
		waitMockLine(t, &out, "Mock for this run at ", func() string {
			return fmt.Sprintf("run did not start its mock: stdout=%q stderr=%q", out.String(), errOut.String())
		})
		cancel()
		select {
		case code := <-exit:
			if code != 130 {
				t.Errorf("cancelled exit = %d, want 130", code)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("cancelled run did not return")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := cli.Run(t.Context(), []string{"run", "--mock", input, "--timeout", "60ms", input}, &out, &errOut)
		if code != 124 {
			t.Errorf("timed-out exit = %d, want 124; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
	})
}
