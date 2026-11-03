package app

// Rule scripts: JavaScript files under ~/.vscroxy/tools/scripts that Script
// rules run on the traffic they match, with the pm API request scripts use.
// The Scripts page of Tools edits them, with a Console of what they printed.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

var scriptsIcon = lucide.FileCode()

// scriptLog is a line a rule script printed, or its error.
type scriptLog struct {
	at                        time.Time
	script, rule, phase, kind string
	text                      string
}

// scriptStore is the rule scripts. The proxy calls Hooks and Run from its
// goroutines, under mu; the rest is the UI thread's.
type scriptStore struct {
	dir string

	mu      sync.RWMutex
	parsed  map[string]*request.ProxyScript // by file name, those that parse
	env     request.ProxyEnv
	console []scriptLog
	// changed tells the UI a script printed; writes hands it what scripts
	// set in pm.globals.
	changed func()
	writes  func([]request.Write)

	names    []string          // without .js, sorted
	texts    map[string]string // as saved
	problems map[string]error  // why a script does not parse
	problem  error             // why the folder could not be read

	choice   string
	list     ui.ListState
	editors  map[string]*editor.State
	name     string // the chosen script's name, as being edited
	nameFor  string
	deleting bool
}

func scriptFile(name string) string { return name + ".js" }

// Hooks implements capture.ScriptRunner.
func (s *scriptStore) Hooks(file string) (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.parsed[file]; p != nil {
		return p.OnRequest, p.OnResponse
	}
	return false, false
}

// Run implements capture.ScriptRunner.
func (s *scriptStore) Run(ctx context.Context, file string, x *capture.ScriptExchange) error {
	s.mu.RLock()
	p, env := s.parsed[file], s.env
	s.mu.RUnlock()
	if p == nil {
		return nil
	}
	out, err := p.Run(ctx, x, env)
	name := strings.TrimSuffix(file, ".js")
	s.mu.Lock()
	for _, m := range out.Console {
		if m.Kind != "variable" {
			s.log(scriptLog{at: time.Now(), script: name, rule: x.Rule, phase: x.Phase, kind: m.Kind, text: m.Text})
		}
	}
	if err != nil {
		s.log(scriptLog{at: time.Now(), script: name, rule: x.Rule, phase: x.Phase, kind: "error", text: err.Error()})
	}
	changed, writes := s.changed, s.writes
	s.mu.Unlock()
	if len(out.Writes) > 0 && writes != nil {
		writes(out.Writes)
	}
	if changed != nil && (len(out.Console) > 0 || err != nil) {
		changed()
	}
	return err
}

// log keeps the last 500 lines; mu is held.
func (s *scriptStore) log(l scriptLog) {
	if len(s.console) >= 500 {
		s.console = slices.Delete(s.console, 0, len(s.console)-499)
	}
	s.console = append(s.console, l)
}

// setEnv gives the scripts what the variables are now, when they changed.
func (s *scriptStore) setEnv(globals, environment map[string]string) {
	s.mu.RLock()
	same := maps.Equal(s.env.Globals, globals) && maps.Equal(s.env.Environment, environment) && s.env.OS != nil
	s.mu.RUnlock()
	if same {
		return
	}
	osEnv := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			osEnv[k] = v
		}
	}
	s.mu.Lock()
	s.env = request.ProxyEnv{Globals: maps.Clone(globals), Environment: maps.Clone(environment), OS: osEnv}
	s.mu.Unlock()
}

// loadScripts reads the scripts folder and hands the scripts that parse to
// the proxy. Drafts of scripts still there survive.
func (a *app) loadScripts() {
	s := &a.scripts
	if s.dir == "" {
		return
	}
	s.changed = a.invalidateTools
	s.writes = func(w []request.Write) {
		if a.win != nil {
			a.win.Update(func() { a.applyWrites(w) })
		}
	}
	files, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.problem = err
		a.fail(s.dir+" could not be read", err)
		return
	}
	s.problem = nil
	s.names, s.texts, s.problems = nil, map[string]string{}, map[string]error{}
	parsed := map[string]*request.ProxyScript{}
	for _, f := range files {
		name, ok := strings.CutSuffix(f.Name(), ".js")
		if !ok || f.IsDir() || capture.CheckScriptName(name) != nil {
			continue
		}
		data, err := a.file(filepath.Join(s.dir, f.Name())).Read()
		if err != nil {
			a.fail("Script "+name+" could not be read", err)
			continue
		}
		s.names = append(s.names, name)
		s.texts[name] = string(data)
		if p, err := request.ParseProxyScript(string(data)); err != nil {
			s.problems[name] = err
		} else {
			parsed[f.Name()] = p
		}
	}
	slices.SortFunc(s.names, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	for name := range s.editors {
		if _, ok := s.texts[name]; !ok {
			delete(s.editors, name)
		}
	}
	s.mu.Lock()
	s.parsed = parsed
	s.mu.Unlock()
	if a.srv != nil {
		a.srv.SetScriptRunner(s)
	}
}

// writeScript saves a script's text and hands it to the proxy.
func (a *app) writeScript(name, text string) error {
	s := &a.scripts
	if s.problem != nil {
		return fmt.Errorf("scripts are read-only until their folder reads: %w", s.problem)
	}
	if err := a.file(filepath.Join(s.dir, scriptFile(name))).Write([]byte(text)); err != nil {
		return err
	}
	if !slices.Contains(s.names, name) {
		s.names = append(s.names, name)
		slices.SortFunc(s.names, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	}
	if s.texts == nil {
		s.texts, s.problems = map[string]string{}, map[string]error{}
	}
	s.texts[name] = text
	p, err := request.ParseProxyScript(text)
	s.mu.Lock()
	if s.parsed == nil {
		s.parsed = map[string]*request.ProxyScript{}
	}
	if err != nil {
		delete(s.parsed, scriptFile(name))
	} else {
		s.parsed[scriptFile(name)] = p
	}
	s.mu.Unlock()
	if err != nil {
		s.problems[name] = err
	} else {
		delete(s.problems, name)
	}
	if a.srv != nil {
		a.srv.SetScriptRunner(s)
	}
	return nil
}

const newScriptText = `// Runs on the exchanges the Script rules using it match, with the pm API.
// Change pm.request before it goes upstream:
function onRequest() {
  pm.request.headers.upsert("X-Debug", "1");
}

// Change pm.response before the client gets it:
function onResponse() {
  // if (pm.response.code === 500) pm.response.code = 200;
}
`

// newScript saves a new script named from base and chooses it.
func (a *app) newScript(base, text string) (string, bool) {
	s := &a.scripts
	name := base
	for i := 2; slices.ContainsFunc(s.names, func(n string) bool { return strings.EqualFold(n, name) }); i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	if err := a.writeScript(name, text); err != nil {
		a.fail("New script", err)
		return "", false
	}
	s.choice, s.nameFor = name, ""
	return name, true
}

// scriptUsers counts the rules that run a script.
func (a *app) scriptUsers(name string) int {
	n := 0
	for _, r := range a.rules {
		if r.Type == capture.RuleScript && r.Target == scriptFile(name) {
			n++
		}
	}
	return n
}

// renameScript renames a script's file and the rules that run it.
func (a *app) renameScript(old, name string) {
	s := &a.scripts
	if name == old {
		return
	}
	err := capture.CheckScriptName(name)
	if err == nil && slices.ContainsFunc(s.names, func(n string) bool { return strings.EqualFold(n, name) && !strings.EqualFold(n, old) }) {
		err = fmt.Errorf("a script named %q exists", name)
	}
	if err != nil {
		a.fail("Rename script", err)
		return
	}
	text := s.texts[old]
	if st := s.editors[old]; st != nil {
		text = st.Buffer.String()
	}
	if err := a.writeScript(name, text); err != nil {
		a.fail("Rename script", err)
		return
	}
	next, renamed := slices.Clone(a.rules), false
	for i, r := range next {
		if r.Type == capture.RuleScript && r.Target == scriptFile(old) {
			next[i].Target, renamed = scriptFile(name), true
		}
	}
	if renamed && !a.applyRuleChanges(next) {
		a.fail("Rename script", errors.New(a.ruleUI.problem))
	}
	if d := &a.ruleUI.draft; d.Type == capture.RuleScript && d.Target == scriptFile(old) {
		d.Target = scriptFile(name)
	}
	a.dropScript(old)
	if st := s.editors[old]; st != nil {
		s.editors[name] = st
		delete(s.editors, old)
	}
	s.choice, s.nameFor = name, ""
}

// dropScript removes a script's file; rules that ran it do nothing.
func (a *app) dropScript(name string) {
	s := &a.scripts
	path := filepath.Join(s.dir, scriptFile(name))
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.fail("Delete script", err)
		return
	}
	delete(a.files, path)
	s.names = slices.DeleteFunc(s.names, func(n string) bool { return n == name })
	delete(s.texts, name)
	delete(s.problems, name)
	delete(s.editors, name)
	s.mu.Lock()
	delete(s.parsed, scriptFile(name))
	s.mu.Unlock()
}

// scriptToSave is the script Save writes: the chosen one with changes.
func (a *app) scriptToSave() (string, bool) {
	s := &a.scripts
	st := s.editors[s.choice]
	return s.choice, st != nil && st.Buffer.String() != s.texts[s.choice]
}

func (a *app) saveScript(name string) {
	st := a.scripts.editors[name]
	if st == nil {
		return
	}
	if err := a.writeScript(name, st.Buffer.String()); err != nil {
		a.fail("Save script", err)
		return
	}
	if err := a.scripts.problems[name]; err != nil {
		a.warn("Script saved · it does not parse, so its rules do nothing: " + err.Error())
		return
	}
	a.done("Script saved · applies to new requests")
}

// openScript shows a script on the Scripts page.
func (a *app) openScript(name string) {
	a.scripts.choice, a.scripts.nameFor = name, ""
	a.openTools("scripts")
}

// scriptSnippets are what Insert snippet offers: rule scripts get their
// hooks; request scripts the rest, which both share.
func scriptSnippets(rule bool) [][2]string {
	shared := [][2]string{
		{"Log a value", "console.log(pm.request.url.toString());\n"},
		{"Read and set a global", "const count = Number(pm.globals.get(\"count\") || 0) + 1;\npm.globals.set(\"count\", count);\n"},
		{"Send a request", "const res = await pm.sendRequest(\"https://example.com/token\");\nconsole.log(res.code, res.json());\n"},
		{"Hash with crypto-js", "const CryptoJS = require(\"crypto-js\");\nconst signature = CryptoJS.HmacSHA256(pm.request.body.raw, \"secret\").toString(CryptoJS.enc.Hex);\n"},
		{"Use lodash", "const _ = require(\"lodash\");\nconst ids = _.map(pm.response.json().items, \"id\");\n"},
	}
	if !rule {
		return shared
	}
	return append([][2]string{
		{"Add a request header", "pm.request.headers.upsert(\"X-Debug\", \"1\");\n"},
		{"Rewrite the URL", "pm.request.url = pm.request.url.toString().replace(\"/v1/\", \"/v2/\");\n"},
		{"Add a query parameter", "pm.request.url.query.upsert(\"debug\", \"1\");\n"},
		{"Read a system environment variable", "const token = process.env.API_TOKEN;\nif (token) pm.request.headers.upsert(\"Authorization\", \"Bearer \" + token);\n"},
		{"Change the status", "if (pm.response.code >= 500) pm.response.code = 200;\n"},
		{"Edit a JSON response", "const body = pm.response.json();\nbody.debug = true;\npm.response.setBody(body);\n"},
		{"Set a response header", "pm.response.headers.upsert(\"Access-Control-Allow-Origin\", \"*\");\n"},
		{"Pass a value from request to response", "// In onRequest:\npm.variables.set(\"startedAt\", Date.now());\n// In onResponse:\nconsole.log(\"took\", Date.now() - Number(pm.variables.get(\"startedAt\")), \"ms\");\n"},
	}, shared...)
}

// snippetMenu inserts a snippet at the caret of st.
func snippetMenu(c *ui.Context, st *editor.State, rule bool) {
	uikit.MenuSelect(c, "Insert snippet", func(m *ui.Menu) {
		for _, s := range scriptSnippets(rule) {
			if m.Item(s[0]).Chosen() {
				st.Insert(s[1])
			}
		}
	}).Label("Insert snippet")
}

// scriptsPage is the scripts beside the chosen one's editor and Console.
func (a *app) scriptsPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	s := &a.scripts
	if !slices.Contains(s.names, s.choice) {
		s.choice = ""
		if len(s.names) > 0 {
			s.choice = s.names[0]
		}
	}
	ui.Column(c).Width(268).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Scripts").FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, "New script", "New script", plusIcon).Clicked() {
				a.newScript("Untitled", newScriptText)
			}
		})
		uikit.Meta(c, "A Script rule runs one on the traffic it matches.")
		if s.problem != nil {
			ui.Text(c, "Read-only: "+s.problem.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3)
		}
		a.scriptList(c, t, active)
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		if s.choice == "" {
			uikit.EmptyState(c, "No scripts", "Change requests and responses with JavaScript and the pm API, for the traffic a Script rule matches.", func() {
				if uikit.PrimaryButton(c, "New script").Clicked() {
					a.newScript("Untitled", newScriptText)
				}
			})
			return
		}
		a.scriptEditor(c, t, s.choice)
	})
	if ui.AlertDialog(c, &s.deleting, "Delete script?", "Rules that run it stay, but do nothing until they run another.", "Cancel", "Delete") == 1 {
		a.dropScript(s.choice)
	}
}

// scriptList is the scripts, with their hooks and how many rules run them.
func (a *app) scriptList(c *ui.Context, t *ui.Theme, active bool) {
	s := &a.scripts
	choice := slices.Index(s.names, s.choice)
	s.list.Selected = &choice
	s.list.Key = func(i int) any { return s.names[i] }
	s.list.Label = func(i int) string { return s.names[i] }
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	c.SetTheme(&pick)
	ui.List(c, &s.list, len(s.names)).Label("Scripts").Grow(1).MinHeight(0).Gap(t.Space(0.5)).Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		name := s.names[row.Index]
		r := ui.Row(c).Height(uikit.HeightLarge+12).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, r, active)
		}
		r.Children(func() {
			uikit.Glyph(c, scriptsIcon)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(uikit.TextBody).TextColor(t.Text).SingleLine()
				if err := s.problems[name]; err != nil {
					ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).SingleLine()
				} else {
					uikit.Meta(c, scriptHooks(s, name)).Font(uikit.FontMono).SingleLine()
				}
			})
			if n := a.scriptUsers(name); n > 0 {
				uikit.Meta(c, fmt.Sprint(n)).Font(uikit.FontMono).FontFeatures("tnum").Tooltip(fmt.Sprintf("Run by %d %s", n, plural(n, "rule", "rules")))
			}
		})
	})
	c.SetTheme(t)
	if choice >= 0 && choice < len(s.names) && s.names[choice] != s.choice {
		s.choice = s.names[choice]
	}
}

func scriptHooks(s *scriptStore, name string) string {
	onRequest, onResponse := s.Hooks(scriptFile(name))
	var hooks []string
	if onRequest {
		hooks = append(hooks, "onRequest")
	}
	if onResponse {
		hooks = append(hooks, "onResponse")
	}
	return cmp.Or(strings.Join(hooks, " · "), "No hooks")
}

// scriptEditor is the chosen script: its name, menu, the code, the Console
// and Save.
func (a *app) scriptEditor(c *ui.Context, t *ui.Theme, name string) {
	s := &a.scripts
	if s.nameFor != name {
		s.name, s.nameFor = name, name
	}
	st := s.editors[name]
	if st == nil {
		buf := editor.NewBuffer(s.texts[name])
		buf.SetLanguage(editor.LanguageNamed("JavaScript"))
		st = &editor.State{Buffer: buf, NoScrollPastEnd: true, Label: "Script " + name}
		if s.editors == nil {
			s.editors = map[string]*editor.State{}
		}
		s.editors[name] = st
	}
	users := a.scriptUsers(name)
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		in := uikit.Input(c, &s.name).Label("Script name").Grow(1).MaxWidth(320).FontSize(uikit.TextTitle).Height(uikit.HeightLarge)
		if in.Submitted() {
			a.renameScript(name, strings.TrimSpace(s.name))
		}
		ui.Spacer(c)
		snippetMenu(c, st, true)
		uikit.MoreMenu(c, "Script actions", func(m *ui.Menu) {
			actionMenu(m, []action{
				do("scripts.rename", "Rename", func() { a.renameScript(name, strings.TrimSpace(s.name)) }).
					when(strings.TrimSpace(s.name) != name, "Type the new name first").in("edit"),
				do("scripts.rule", "New rule running it", func() {
					a.editRule(-1, capture.Rule{Name: name, Type: capture.RuleScript, Target: scriptFile(name)})
					a.openTools("rules")
				}).in("edit"),
				do("scripts.delete", "Delete…", func() { s.deleting = true }).in("danger"),
			})
		})
	})
	// Where it does not parse.
	version := st.Buffer.Version()
	if err, ok := s.problems[name].(request.ScriptProblem); ok && st.Buffer.String() == s.texts[name] {
		line := max(err.Line-1, 0)
		start := st.Buffer.Offset(editor.Pos{Line: line})
		st.SetDiagnostics(version, []editor.Diagnostic{{Start: start, End: start + len(st.Buffer.Line(line)), Severity: editor.SeverityError, Message: err.Message}})
	} else {
		st.SetDiagnostics(version, nil)
	}
	st.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	st.Theme = uikit.EditorTheme(t)
	ui.Box(c).Grow(1).MinHeight(120).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		editor.View(c, st).Grow(1).FillWidth().FocusRing(true)
	})
	a.scriptConsole(c, t, name)
	_, modified := a.scriptToSave()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		note := "No rule runs it yet; add a Script rule in Rules."
		if users > 0 {
			note = fmt.Sprintf("Run by %d %s · changes apply to new requests.", users, plural(users, "rule", "rules"))
		}
		uikit.Meta(c, note).Grow(1).SingleLine()
		ui.Row(c).Disabled(!modified).Children(func() {
			if uikit.PrimaryButton(c, "Save").Tooltip("Save · Ctrl/Cmd+S").Clicked() {
				a.saveScript(name)
			}
		})
	})
	if modified {
		c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveScript(name) })
	}
}

// scriptConsole is what the chosen script printed, newest last.
func (a *app) scriptConsole(c *ui.Context, t *ui.Theme, name string) {
	s := &a.scripts
	s.mu.RLock()
	var lines []scriptLog
	for _, l := range s.console {
		if l.script == name {
			lines = append(lines, l)
		}
	}
	s.mu.RUnlock()
	ui.Column(c).Height(150).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		ui.Row(c).Height(uikit.HeightCompact+4).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Label(c, "Console").Grow(1)
			if uikit.CompactButton(c, "Clear").Disabled(len(lines) == 0).Clicked() {
				s.mu.Lock()
				s.console = slices.DeleteFunc(s.console, func(l scriptLog) bool { return l.script == name })
				s.mu.Unlock()
			}
		})
		ui.Divider(c)
		ui.Scroll(c.Key("script-console-"+name)).Grow(1).FillWidth().Padding(t.Space(1), t.Space(2)).Gap(t.Space(1)).Children(func() {
			if len(lines) == 0 {
				uikit.Hint(c, "console.log(...) and errors show here as its rules run.")
			}
			for _, l := range lines {
				color := t.Text
				switch l.kind {
				case "error":
					color = t.Danger
				case "warning":
					color = t.Warning
				}
				text := fmt.Sprintf("%s  %s · %s  %s", l.at.Format("15:04:05"), l.rule, l.phase, l.text)
				ui.Text(c, text).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(color).Selectable().FillWidth()
			}
		})
	})
}
