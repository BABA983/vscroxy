package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/system"
)

// The project folder is where collections live for Git collaboration. This
// file runs the git commands the Tools → Git page needs; the page itself is in
// git_ui.go. Commands run without a shell, with prompts and pagers off, and
// paths always follow "--".

var (
	errGitMissing = errors.New("git is not installed")
	errNotGitRepo = errors.New("not a Git repository")
)

const (
	// gitStatusLimit bounds the changes the page lists and commits. A working
	// tree with more changes is not a panel's job to review.
	gitStatusLimit = 500
	// gitDiffLimit bounds one diff or new-file preview. A larger diff is cut
	// and says so instead of growing the window without limit.
	gitDiffLimit = 1 << 20
)

// gitFile is one changed path in the working tree. Code is the two-letter
// porcelain status; Orig is the old path of a rename or copy.
type gitFile struct {
	Path     string
	Orig     string
	Code     string
	Selected bool
}

// gitWorkingTree is the project folder's repository and its changes.
type gitWorkingTree struct {
	Root     string
	Branch   string
	Detached string
	Files    []gitFile
	More     bool
}

// gitCommand runs git in dir. stdout receives the output; stderr becomes the
// error, because git's own explanation is what a user needs.
func gitCommand(dir string, stdout io.Writer, args ...string) error {
	git, err := system.LookPath(context.Background(), "git")
	if err != nil {
		return errGitMissing
	}
	cmd := exec.Command(git, append([]string{"--no-pager", "-c", "color.ui=false", "-C", dir}, args...)...)
	// A parent process may carry GIT_DIR, GIT_WORK_TREE or GIT_INDEX_FILE from
	// a rebase --exec or another checkout. The panel always acts on dir, so
	// none of those may reach git.
	cmd.Env = gitEnvironment()
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

// gitEnvironment is the inherited and login-shell environment without every
// GIT_* variable, plus the settings the panel always wants. A Git panel must
// not inherit another repository's GIT_DIR or index.
func gitEnvironment() []string {
	env := slices.DeleteFunc(system.ShellEnv(context.Background()), func(variable string) bool {
		return strings.HasPrefix(strings.ToUpper(variable), "GIT_")
	})
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
}

// gitOutput runs a command whose output is small enough to keep whole.
func gitOutput(dir string, args ...string) (string, error) {
	var out bytes.Buffer
	if err := gitCommand(dir, &out, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// gitStatus reads the repository root, the current branch and the changed
// paths of the project folder.
func gitStatus(dir string) (gitWorkingTree, error) {
	if _, err := system.LookPath(context.Background(), "git"); err != nil {
		return gitWorkingTree{}, errGitMissing
	}
	root, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
			return gitWorkingTree{}, errNotGitRepo
		}
		return gitWorkingTree{}, err
	}
	if root == "" {
		return gitWorkingTree{}, errNotGitRepo
	}
	tree := gitWorkingTree{Root: root}
	if branch, err := gitOutput(root, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		tree.Branch = branch
	}
	if tree.Branch == "" {
		if short, err := gitOutput(root, "rev-parse", "--short", "HEAD"); err == nil {
			tree.Detached = short
		}
	}
	var status bytes.Buffer
	if err := gitCommand(root, &status, "status", "--porcelain=v1", "-z", "--untracked-files=all"); err != nil {
		return gitWorkingTree{}, err
	}
	tree.Files, tree.More = parseGitStatus(status.String())
	return tree, nil
}

// parseGitStatus reads porcelain v1 -z output. A rename or copy carries its
// old path as the next NUL-separated field.
func parseGitStatus(out string) ([]gitFile, bool) {
	var files []gitFile
	for len(out) >= 3 && len(files) < gitStatusLimit {
		code := out[:2]
		if out[2] != ' ' {
			break
		}
		out = out[3:]
		path, rest, ok := strings.Cut(out, "\x00")
		if !ok {
			break
		}
		out = rest
		file := gitFile{Path: path, Code: code}
		if strings.ContainsAny(code, "RC") {
			if orig, rest, ok := strings.Cut(out, "\x00"); ok {
				file.Orig, out = orig, rest
			}
		}
		files = append(files, file)
	}
	return files, len(out) >= 3
}

// gitDiff is one file's change against HEAD, or a new-file preview for an
// untracked file. Cut says the text hit gitDiffLimit.
func gitDiff(root string, file gitFile) (text string, cut bool, err error) {
	if file.Code == "??" {
		return gitNewFile(root, file.Path)
	}
	paths := []string{file.Path}
	if file.Orig != "" {
		paths = append(paths, file.Orig)
	}
	args := append([]string{"diff", "HEAD", "--no-color", "--no-ext-diff", "--"}, paths...)
	capped := &cappedBuffer{limit: gitDiffLimit}
	err = gitCommand(root, capped, args...)
	if err != nil {
		// An unborn HEAD has nothing to diff against: show the staged and
		// unstaged changes against the empty index instead.
		var staged, worktree cappedBuffer
		staged.limit, worktree.limit = gitDiffLimit, gitDiffLimit
		if cachedErr := gitCommand(root, &staged, append([]string{"diff", "--cached", "--no-color", "--no-ext-diff", "--"}, paths...)...); cachedErr != nil {
			return "", false, err
		}
		if workErr := gitCommand(root, &worktree, append([]string{"diff", "--no-color", "--no-ext-diff", "--"}, paths...)...); workErr != nil {
			return "", false, err
		}
		return staged.buf.String() + worktree.buf.String(), staged.cut || worktree.cut, nil
	}
	return capped.buf.String(), capped.cut, nil
}

// gitNewFile renders an untracked file as the diff git would show for it.
// Binary files and files over the limit are named rather than shown.
func gitNewFile(root, path string) (string, bool, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Stat(full)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("Not a regular file: %s\n", path), false, nil
	}
	if info.Size() > gitDiffLimit {
		return fmt.Sprintf("New file: %s (%d bytes; too large to preview)\n", path, info.Size()), true, nil
	}
	file, err := os.Open(full)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, gitDiffLimit+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > gitDiffLimit {
		return fmt.Sprintf("New file: %s (too large to preview)\n", path), true, nil
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return fmt.Sprintf("Binary file: %s (%d bytes)\n", path, len(data)), false, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", path, path, path)
	body := strings.TrimSuffix(string(data), "\n")
	if body != "" {
		lines := strings.Count(body, "\n") + 1
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", lines)
		for line := range strings.SplitSeq(body, "\n") {
			b.WriteString("+" + line + "\n")
		}
	}
	return b.String(), false, nil
}

// gitCommit stages and commits the selected paths. Other staged changes stay
// out: the pathspec limits the commit to what the panel showed selected.
func gitCommit(root string, files []gitFile, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("enter a commit message")
	}
	if len(files) == 0 {
		return errors.New("select at least one file")
	}
	var add, commit []string
	for _, file := range files {
		add = append(add, file.Path)
		commit = append(commit, file.Path)
		if file.Orig != "" {
			// A rename is already staged by git mv; adding its missing old
			// path would fail. The commit names both so the deletion lands
			// with the new name.
			commit = append(commit, file.Orig)
		}
	}
	if err := gitCommand(root, io.Discard, append([]string{"add", "-A", "--"}, add...)...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := gitCommand(root, io.Discard, append([]string{"commit", "-m", message, "--"}, commit...)...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// gitInit makes the project folder a repository.
func gitInit(dir string) error {
	return gitCommand(dir, io.Discard, "init")
}

// cappedBuffer keeps at most limit bytes and records that it cut the rest.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
	cut   bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
			w.cut = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.cut = true
	}
	return len(p), nil
}
