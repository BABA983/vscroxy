package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// gitPanel is Tools → Git: the project folder's status, one file's diff and a
// commit of the selected files. Git runs off the UI thread; every result is
// checked against the folder, root and path it was started for, so a late
// answer cannot replace a newer one.
type gitPanel struct {
	dir      string
	root     string
	branch   string
	detached string
	files    []gitFile
	more     bool

	loaded  bool
	loading bool
	// problem is "missing-git", "not-a-repo", or git's own message.
	problem string

	chosen      string
	diff        string
	diffCut     bool
	diffBusy    bool
	diffProblem string

	list       ui.ListState
	message    string
	committing bool
}

var gitIcon = lucide.GitBranch()

// resetGit forgets a project folder's panel, so the next draw reads the new
// one from scratch.
func (a *app) resetGit() {
	a.git = gitPanel{}
}

// refreshGit reads the working tree in the background and publishes it on the
// UI thread.
func (a *app) refreshGit() {
	dir := a.projectDir
	if dir == "" || a.git.loading {
		return
	}
	a.git.loading, a.git.problem = true, ""
	load := func() {
		tree, err := gitStatus(dir)
		apply := func() {
			// The Tools window is not the window Update repaints.
			defer a.invalidateTools()
			if a.projectDir != dir {
				return
			}
			a.git.loading = false
			if a.git.dir != dir {
				a.git = gitPanel{dir: dir, loading: false}
			}
			switch {
			case errors.Is(err, errGitMissing):
				a.git.problem, a.git.loaded = "missing-git", true
				return
			case errors.Is(err, errNotGitRepo):
				a.git.problem, a.git.loaded = "not-a-repo", true
				return
			case err != nil:
				a.git.problem, a.git.loaded = err.Error(), true
				return
			}
			chosen := map[string]bool{}
			for _, file := range a.git.files {
				if file.Selected {
					chosen[file.Path] = true
				}
			}
			for i := range tree.Files {
				tree.Files[i].Selected = chosen[tree.Files[i].Path]
			}
			a.git.root, a.git.branch, a.git.detached = tree.Root, tree.Branch, tree.Detached
			a.git.files, a.git.more, a.git.loaded, a.git.problem = tree.Files, tree.More, true, ""
			if a.git.chosen != "" && !slices.ContainsFunc(a.git.files, func(f gitFile) bool { return f.Path == a.git.chosen }) {
				a.git.chosen, a.git.diff, a.git.diffProblem = "", "", ""
			}
			if a.git.chosen == "" && len(a.git.files) > 0 {
				a.chooseGitFile(a.git.files[0].Path)
			}
		}
		if a.win == nil {
			apply()
			return
		}
		a.win.Update(apply)
	}
	if a.win == nil {
		load()
		return
	}
	go load()
}

// chooseGitFile shows one file's diff. A result for another path, or for
// another repository root, is dropped.
func (a *app) chooseGitFile(path string) {
	index := slices.IndexFunc(a.git.files, func(f gitFile) bool { return f.Path == path })
	if index < 0 {
		return
	}
	a.git.chosen, a.git.diff, a.git.diffCut = path, "", false
	a.git.diffProblem, a.git.diffBusy = "", true
	file, root := a.git.files[index], a.git.root
	show := func() {
		text, cut, err := gitDiff(root, file)
		apply := func() {
			defer a.invalidateTools()
			if a.git.chosen != path || a.git.root != root {
				return
			}
			a.git.diffBusy = false
			if err != nil {
				a.git.diffProblem = err.Error()
				return
			}
			a.git.diff, a.git.diffCut = text, cut
		}
		if a.win == nil {
			apply()
			return
		}
		a.win.Update(apply)
	}
	if a.win == nil {
		show()
		return
	}
	go show()
}

// commitGit commits the selected files with the message the page holds.
func (a *app) commitGit() {
	if a.git.committing || a.git.root == "" {
		return
	}
	var files []gitFile
	for _, file := range a.git.files {
		if file.Selected {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		a.warn(locales.GitSelectAtLeastOneFile())
		return
	}
	if strings.TrimSpace(a.git.message) == "" {
		a.warn(locales.GitEnterACommitMessage())
		return
	}
	root, message := a.git.root, a.git.message
	a.git.committing = true
	commit := func() {
		err := gitCommit(root, files, message)
		apply := func() {
			a.git.committing = false
			if err != nil {
				a.fail(locales.GitCommit(), err)
				return
			}
			a.git.message = ""
			a.done(locales.GitCommitted(len(files)))
			a.refreshGit()
		}
		if a.win == nil {
			apply()
			return
		}
		a.win.Update(apply)
	}
	if a.win == nil {
		commit()
		return
	}
	go commit()
}

// initGit makes the project folder a repository, then reads it again.
func (a *app) initGit() {
	dir := a.projectDir
	if dir == "" || a.git.loading {
		return
	}
	a.git.loading = true
	init := func() {
		err := gitInit(dir)
		apply := func() {
			a.git.loading = false
			if a.projectDir != dir {
				return
			}
			if err != nil {
				a.fail(locales.GitInitializeRepository(), err)
				return
			}
			a.resetGit()
			a.refreshGit()
		}
		if a.win == nil {
			apply()
			return
		}
		a.win.Update(apply)
	}
	if a.win == nil {
		init()
		return
	}
	go init()
}

// gitPage is Tools → Git.
func (a *app) gitPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	if a.projectDir == "" {
		uikit.EmptyState(c, locales.GitNoProjectFolder(), locales.GitNoProjectFolderHint(), func() {
			if uikit.SecondaryButton(c, locales.OpenSettings()).Clicked() {
				a.openSettings(0)
			}
		})
		return
	}
	if a.git.dir != a.projectDir && !a.git.loading {
		a.refreshGit()
	}
	if a.git.problem == "missing-git" || a.git.problem == "not-a-repo" {
		a.gitProblem(c, t)
		return
	}
	ui.Column(c).Width(330).MinHeight(0).Padding(top, t.Space(uikit.SpaceL), t.Space(uikit.SpaceL), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			uikit.Heading(c, locales.Git()).TextColor(t.Text).Grow(1)
			if uikit.RowIconButton(c, locales.Refresh(), locales.Refresh(), lucide.RefreshCw()).Disabled(a.git.loading).Clicked() {
				a.refreshGit()
			}
		})
		uikit.Meta(c, a.projectDir).SingleLine()
		if a.git.loading && !a.git.loaded {
			uikit.Meta(c, locales.GitLoadingChanges())
			return
		}
		if a.git.problem != "" {
			uikit.ErrorText(c, a.git.problem).MaxLines(4)
			return
		}
		switch {
		case a.git.branch != "":
			uikit.Meta(c, locales.GitOnBranch(a.git.branch))
		case a.git.detached != "":
			uikit.Meta(c, locales.GitDetachedAt(a.git.detached))
		}
		if a.git.more {
			uikit.Meta(c, locales.GitOnlyTheFirstChangesAreShown(gitStatusLimit))
		}
		a.gitFileList(c, t, active)
		c.Key("git-commit-message")
		uikit.TextArea(c, &a.git.message).Label(locales.GitCommitMessage()).
			Placeholder(locales.GitCommitMessageHint()).Height(72).Disabled(a.git.committing)
		if uikit.PrimaryButton(c, locales.GitCommitSelected()).Disabled(a.git.committing || !a.gitHasSelection()).Clicked() {
			a.commitGit()
		}
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(uikit.SpaceXL), t.Space(uikit.SpaceL), t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
		file, ok := a.gitChosenFile()
		if !ok {
			uikit.EmptyState(c, locales.GitSelectAChangedFile(), locales.GitSelectAChangedFileHint(), func() {})
			return
		}
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.Caption(c, gitStatusLabel(file.Code)).TextColor(gitStatusColor(t, file.Code))
			uikit.DataBody(c, file.Path).TextColor(t.Text).Grow(1).SingleLine()
		})
		if file.Orig != "" {
			uikit.Meta(c, locales.GitRenamedFrom(file.Orig)).Font(uikit.FontMono).SingleLine()
		}
		if a.git.diffProblem != "" {
			uikit.ErrorText(c, a.git.diffProblem).MaxLines(4)
			return
		}
		if a.git.diffBusy {
			uikit.Meta(c, locales.GitLoadingDiff())
		}
		if a.git.diffCut {
			uikit.Meta(c, locales.GitDiffIsCutAt1Mib())
		}
		c.Key("git-diff-" + file.Path)
		uikit.TextArea(c, &a.git.diff).ReadOnly(true).Font(uikit.FontMono).
			Label(locales.GitDiff()).Grow(1).MinHeight(0).FillWidth()
	})
}

// gitProblem is the page without a repository: missing git, or a folder to
// initialize.
func (a *app) gitProblem(c *ui.Context, t *ui.Theme) {
	title, hint := locales.GitNotInstalled(), locales.GitInstallGitAndRefresh()
	actions := func() {
		if uikit.SecondaryButton(c, locales.Refresh()).Clicked() {
			a.refreshGit()
		}
	}
	if a.git.problem == "not-a-repo" {
		title, hint = locales.GitThisFolderIsNotARepository(), locales.GitInitializeOneHereOrChoose()
		actions = func() {
			if uikit.PrimaryButtonBusy(c, locales.GitInitializeRepository(), a.git.loading).Clicked() {
				a.initGit()
			}
			if uikit.SecondaryButton(c, locales.Refresh()).Clicked() {
				a.refreshGit()
			}
		}
	}
	uikit.EmptyState(c, title, hint, actions)
}

// gitFileList is the changed files with a checkbox for each one to commit.
func (a *app) gitFileList(c *ui.Context, t *ui.Theme, active bool) {
	files := a.git.files
	choice := slices.IndexFunc(files, func(f gitFile) bool { return f.Path == a.git.chosen })
	a.git.list.Selected = &choice
	a.git.list.Key = func(i int) any { return files[i].Path }
	a.git.list.Label = func(i int) string { return files[i].Path }
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	pick.SurfaceHover = ui.Color{}
	c.SetTheme(&pick)
	list := ui.List(c, &a.git.list, len(files)).Label(locales.GitChangedFiles()).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceHair)).Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		file := &a.git.files[row.Index]
		r := ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(uikit.SpaceS)).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, r, active)
		}
		r.Children(func() {
			uikit.Checkbox(c, &file.Selected, "").Label(locales.GitSelectFile(file.Path))
			uikit.DataMeta(c, gitLetter(file.Code)).
				TextColor(gitStatusColor(t, file.Code)).Width(18)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				uikit.Body(c, file.Path).TextColor(t.Text).SingleLine()
				if file.Orig != "" {
					uikit.Meta(c, locales.GitRenamedFrom(file.Orig)).SingleLine()
				}
			})
		})
	})
	c.SetTheme(t)
	if list.Changed() && choice >= 0 && choice < len(files) && files[choice].Path != a.git.chosen {
		a.chooseGitFile(files[choice].Path)
	}
}

func (a *app) gitHasSelection() bool {
	return slices.ContainsFunc(a.git.files, func(f gitFile) bool { return f.Selected })
}

func (a *app) gitChosenFile() (gitFile, bool) {
	i := slices.IndexFunc(a.git.files, func(f gitFile) bool { return f.Path == a.git.chosen })
	if i < 0 {
		return gitFile{}, false
	}
	return a.git.files[i], true
}

// gitLetter is the one letter a row shows: the staged status when there is
// one, else the working tree's. Conflicts and untracked files have their own.
func gitLetter(code string) string {
	switch {
	case code == "??":
		return "U"
	case strings.ContainsRune(code, 'U') || code == "AA" || code == "DD":
		return "!"
	case code[0] != ' ':
		return string(code[0])
	default:
		return string(code[1])
	}
}

func gitStatusLabel(code string) string {
	switch {
	case code == "??":
		return locales.GitNewFile()
	case strings.ContainsRune(code, 'U') || code == "AA" || code == "DD":
		return locales.GitConflict()
	}
	letter := gitLetter(code)
	switch letter {
	case "A":
		return locales.GitAdded()
	case "C":
		return locales.GitCopied()
	case "D":
		return locales.GitDeleted()
	case "M":
		return locales.GitModified()
	case "R":
		return locales.GitRenamed()
	case "T":
		return locales.GitTypeChanged()
	default:
		return locales.ChangedLetter(letter)
	}
}

func gitStatusColor(t *ui.Theme, code string) ui.Color {
	p := uikit.Colors(t)
	switch {
	case code == "??" || gitLetter(code) == "A":
		return p.Success
	case gitLetter(code) == "D" || gitLetter(code) == "!":
		return p.Danger
	case gitLetter(code) == "R" || gitLetter(code) == "C":
		return p.Info
	default:
		return p.Warning
	}
}
