package cli_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/repro"
	"github.com/baba983/vscroxy/internal/request"
)

func TestRunReproBundle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	collection, _, err := postman.WriteCollection("Repro", []request.Request{{
		ID: "repro-1", Name: "GET /ok", Collection: "Repro", Method: "GET", URL: upstream.URL + "/ok",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var embedded any
	if err := json.Unmarshal(collection, &embedded); err != nil {
		t.Fatal(err)
	}
	bundle := map[string]any{
		"format": repro.Format, "version": repro.Version, "title": "Repro",
		"flows": []any{}, "collection": embedded,
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repro.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"run", path}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "1 completed · 0 failed") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
}
