package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// gitTestEnvironment is os.Environ without every GIT_* variable, plus the
// test's own identity. A linked worktree's git rebase --exec exports GIT_DIR;
// without this filter the test would configure and commit in the real
// repository instead of its temporary one.
func gitTestEnvironment() []string {
	env := slices.DeleteFunc(os.Environ(), func(variable string) bool {
		return strings.HasPrefix(strings.ToUpper(variable), "GIT_")
	})
	return append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
}

// gitRepo is a temporary repository with a fixed identity, so a test's
// commits never depend on the machine's git configuration.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = gitTestEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	// gitCommit runs through the app's command, which inherits the process
	// environment, so the identity must live in the repository itself.
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	return dir
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGitStatusAndCommitSelectedFiles(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	writeFile(t, dir, "b.txt", "two\n")
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if tree.Root != real || tree.Branch != "main" {
		t.Fatalf("root %q branch %q", tree.Root, tree.Branch)
	}
	if len(tree.Files) != 2 || tree.Files[0].Path != "a.txt" || tree.Files[0].Code != "??" {
		t.Fatalf("files = %+v", tree.Files)
	}
	if err := gitCommit(tree.Root, tree.Files[:1], "add a"); err != nil {
		t.Fatal(err)
	}
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "b.txt" || tree.Files[0].Code != "??" {
		t.Fatalf("after commit = %+v", tree.Files)
	}
	writeFile(t, dir, "a.txt", "one changed\n")
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 2 || tree.Files[0].Path != "a.txt" || tree.Files[0].Code != " M" {
		t.Fatalf("after edit = %+v", tree.Files)
	}
	if err := gitCommit(tree.Root, tree.Files[:1], "edit a"); err != nil {
		t.Fatal(err)
	}
	// The commit names only the selected file.
	log, err := gitOutput(dir, "show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(log) != "a.txt" {
		t.Fatalf("commit touched %q", log)
	}
}

func TestGitStatusKeepsSpacesAndRenameSource(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a file.txt", "one\n")
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "a file.txt" {
		t.Fatalf("files = %+v", tree.Files)
	}
	if err := gitCommit(tree.Root, tree.Files, "add"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "mv", "a file.txt", "b file.txt")
	cmd.Env = gitTestEnvironment()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v\n%s", err, out)
	}
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "b file.txt" || tree.Files[0].Orig != "a file.txt" || tree.Files[0].Code != "R " {
		t.Fatalf("rename = %+v", tree.Files)
	}
	if err := gitCommit(tree.Root, tree.Files, "rename"); err != nil {
		t.Fatal(err)
	}
	if tree, err = gitStatus(dir); err != nil || len(tree.Files) != 0 {
		t.Fatalf("after rename commit: %+v, %v", tree.Files, err)
	}
}

func TestGitDiffTracksEditsAndShowsNewFiles(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "old\n")
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	diff, cut, err := gitDiff(tree.Root, tree.Files[0])
	if err != nil || cut {
		t.Fatalf("new file diff: %v cut=%v", err, cut)
	}
	if !strings.Contains(diff, "new file mode") || !strings.Contains(diff, "+old") {
		t.Fatalf("new file diff = %q", diff)
	}
	if err := gitCommit(tree.Root, tree.Files, "add"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.txt", "new\n")
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	diff, _, err = gitDiff(tree.Root, tree.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "-old") || !strings.Contains(diff, "+new") {
		t.Fatalf("diff = %q", diff)
	}
	writeFile(t, dir, "raw.bin", "\x00\xff")
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin := tree.Files[slices.IndexFunc(tree.Files, func(f gitFile) bool { return f.Path == "raw.bin" })]
	diff, _, err = gitDiff(tree.Root, bin)
	if err != nil || !strings.Contains(diff, "Binary file") {
		t.Fatalf("binary diff = %q, %v", diff, err)
	}
}

func TestGitDiffIsCutAtTheLimit(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "big.txt", strings.Repeat("a\n", 100))
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitCommit(tree.Root, tree.Files, "add"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "big.txt", strings.Repeat("b\n", gitDiffLimit))
	tree, err = gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	diff, cut, err := gitDiff(tree.Root, tree.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !cut || len(diff) > gitDiffLimit {
		t.Fatalf("cut=%v len=%d", cut, len(diff))
	}
}

func TestGitStatusOutsideARepository(t *testing.T) {
	if _, err := gitStatus(t.TempDir()); !errors.Is(err, errNotGitRepo) {
		t.Fatalf("err = %v", err)
	}
}

func TestGitCommitNeedsAMessageAndSelection(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitCommit(tree.Root, tree.Files, "  "); err == nil {
		t.Fatal("an empty message was accepted")
	}
	if err := gitCommit(tree.Root, nil, "message"); err == nil {
		t.Fatal("an empty selection was accepted")
	}
}

func TestGitInitMakesTheFolderARepository(t *testing.T) {
	dir := t.TempDir()
	if err := gitInit(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := gitStatus(dir); err != nil {
		t.Fatal(err)
	}
}

func TestParseGitStatusRenamesKeepTheirSource(t *testing.T) {
	files, more := parseGitStatus("R  new name.txt\x00old name.txt\x00 M plain.txt\x00")
	if more {
		t.Fatal("reported more")
	}
	if len(files) != 2 || files[0].Path != "new name.txt" || files[0].Orig != "old name.txt" || files[1].Path != "plain.txt" || files[1].Code != " M" {
		t.Fatalf("files = %+v", files)
	}
}

// TestGitPanelCommitsTheSelectedFile drives the Tools page: the status list,
// the checkbox, the message and the commit button.
func TestGitPanelCommitsTheSelectedFile(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	writeFile(t, dir, "b.txt", "two\n")
	a := workspaceApp()
	a.projectDir, a.projectInput = dir, dir
	a.openTools("git")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("On branch main") || !tt.HasText("a.txt") || !tt.HasText("b.txt") {
		t.Fatalf("panel = %q", tt.Texts())
	}
	if err := tt.Click("Select a.txt to commit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleTextField, "Commit message")
	if !ok {
		t.Fatal("no commit message field")
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	tt.Type("add a")
	tt.Frame()
	if err := tt.Click("Commit selected"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "b.txt" || tree.Files[0].Code != "??" {
		t.Fatalf("after commit = %+v", tree.Files)
	}
	if subject, err := gitOutput(dir, "log", "-1", "--format=%s"); err != nil || subject != "add a" {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	if a.git.message != "" || !tt.HasText("Committed 1 file.") {
		t.Fatalf("message %q, texts %q", a.git.message, tt.Texts())
	}
}

// A rebase --exec in a linked worktree exports GIT_DIR (and sometimes
// GIT_INDEX_FILE). The panel and its test helper must act on their own folder,
// never on the repository those variables name.
func TestGitIgnoresInheritedRepositoryVariables(t *testing.T) {
	other := gitRepo(t)
	index := filepath.Join(t.TempDir(), "inherited-index")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_INDEX_FILE", index)

	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	tree, err := gitStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if tree.Root != real {
		t.Fatalf("status root = %q, want %q", tree.Root, real)
	}
	if err := gitCommit(tree.Root, tree.Files, "add a"); err != nil {
		t.Fatal(err)
	}
	if subject, err := gitOutput(dir, "log", "-1", "--format=%s"); err != nil || subject != "add a" {
		t.Fatalf("temporary repository log = %q, %v", subject, err)
	}
	if status, err := gitOutput(other, "status", "--porcelain"); err != nil || status != "" {
		t.Fatalf("other repository changed: %q, %v", status, err)
	}
	if _, err := os.Stat(index); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inherited index was used: %v", err)
	}
}
