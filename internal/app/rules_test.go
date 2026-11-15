package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestRuleListGroupsNestedFolders(t *testing.T) {
	a := workspaceApp()
	a.rules = []capture.Rule{
		{Name: "root", Type: capture.RuleBlock, Match: "/root"},
		{Name: "auth", Type: capture.RuleBlock, Match: "/auth", Folder: "API/Auth"},
		{Name: "users", Type: capture.RuleBlock, Match: "/users", Folder: "API"},
		{Name: "other", Type: capture.RuleBlock, Match: "/other", Folder: "Other"},
	}
	rows := a.ruleListRows()
	want := []struct {
		folder string
		depth  int
		index  int
	}{{"", 0, 0}, {"API", 1, -1}, {"", 1, 2}, {"API/Auth", 2, -1}, {"", 2, 1}, {"Other", 1, -1}, {"", 1, 3}}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i, w := range want {
		if rows[i].folder != w.folder || rows[i].depth != w.depth || rows[i].index != w.index {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
	if a.folderCount("API") != 2 || a.folderCount("API/Auth") != 1 || a.folderCount("Other") != 1 {
		t.Fatal("folder counts do not include nested rules")
	}
	a.ruleUI.collapsed = map[string]bool{"API": true}
	rows = a.ruleListRows()
	want = []struct {
		folder string
		depth  int
		index  int
	}{{"", 0, 0}, {"API", 1, -1}, {"Other", 1, -1}, {"", 1, 3}}
	if len(rows) != len(want) {
		t.Fatalf("collapsed rows: %+v", rows)
	}
	for i, w := range want {
		if rows[i].folder != w.folder || rows[i].depth != w.depth || rows[i].index != w.index {
			t.Fatalf("collapsed row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}
