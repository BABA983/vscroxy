package app

// Rule scripts: JavaScript files under ~/.vscroxy/tools/scripts that Script
// rules run on the traffic they match, with the pm API request scripts use.
// Addons, under ~/.vscroxy/tools/addons, are modules request and rule scripts
// load with require("name"). The Scripts page of Tools edits both, the rule
// scripts with a Console of what they printed.

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
	dir   string
	addon bool // the addons, not the rule scripts

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
	if p == nil && x.Phase != "mock" {
		return nil
	}
	var out request.ProxyRun
	var err error
	if p == nil {
		err = errors.New("Code Mock script is missing or does not parse")
	} else {
		out, err = p.Run(ctx, x, env)
	}
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

// setEnv gives the scripts what the variables and addons are now, when they
// changed.
func (s *scriptStore) setEnv(globals, environment, addons map[string]string) {
	s.mu.RLock()
	same := maps.Equal(s.env.Globals, globals) && maps.Equal(s.env.Environment, environment) && maps.Equal(s.env.Addons, addons) && s.env.OS != nil
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
	s.env = request.ProxyEnv{Globals: maps.Clone(globals), Environment: maps.Clone(environment), OS: osEnv, Addons: addons}
	s.mu.Unlock()
}

// parse converts a rule script and finds its hooks, or checks that an
// addon converts; an addon has no hooks, so no ProxyScript.
func (s *scriptStore) parse(name, text string) (*request.ProxyScript, error) {
	if s.addon {
		return nil, request.CheckModule(name, text)
	}
	return request.ParseProxyScript(name, text)
}

// sources are the addons that parse, by name, as scripts require them.
func (s *scriptStore) sources() map[string]string {
	out := map[string]string{}
	for name, text := range s.texts {
		if s.problems[name] == nil {
			out[name] = text
		}
	}
	return out
}

// shownScripts is what the Scripts page shows: the rule scripts or the
// addons.
func (a *app) shownScripts() *scriptStore {
	if a.addonsShown {
		return &a.addons
	}
	return &a.scripts
}

// loadScripts reads the scripts and addons folders and hands the scripts
// that parse to the proxy. Drafts of files still there survive.
func (a *app) loadScripts() {
	a.addons.addon = true
	a.loadScriptDir(&a.addons)
	a.loadScriptDir(&a.scripts)
}

func (a *app) loadScriptDir(s *scriptStore) {
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
		check := capture.CheckScriptName
		if s.addon {
			check = checkAddonName
		}
		if !ok || f.IsDir() || check(name) != nil {
			continue
		}
		data, err := a.file(filepath.Join(s.dir, f.Name())).Read()
		if err != nil {
			a.fail("Script "+name+" could not be read", err)
			continue
		}
		s.names = append(s.names, name)
		s.texts[name] = string(data)
		if p, err := s.parse(name, string(data)); err != nil {
			s.problems[name] = err
		} else if p != nil {
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
	if a.srv != nil && !s.addon {
		a.srv.SetScriptRunner(s)
	}
}

// writeScript saves a script's or addon's text and hands it to the proxy.
func (a *app) writeScript(s *scriptStore, name, text string) error {
	check := capture.CheckScriptName
	if s.addon {
		check = checkAddonName
	}
	if err := check(name); err != nil {
		return err
	}
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
	p, err := s.parse(name, text)
	s.mu.Lock()
	if s.parsed == nil {
		s.parsed = map[string]*request.ProxyScript{}
	}
	if err != nil || p == nil {
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
	if a.srv != nil && !s.addon {
		a.srv.SetScriptRunner(s)
	}
	return nil
}

const newScriptText = `// Runs on the exchanges the Script rules using it match, with the pm API.
// import works for lodash, crypto-js, chai and your addons.

// Change pm.request before it goes upstream:
export function onRequest() {
  pm.request.headers.upsert("X-Debug", "1");
}

// Change pm.response before the client gets it:
export function onResponse() {
  // if (pm.response.code === 500) pm.response.code = 200;
}
`

const newMockScriptText = `// Select this script in a Code Mock rule (Tools → Mocks or Rules).
// Reads a complete text request and answers locally, without upstream I/O.
// Errors return a local 502; pm.sendRequest is unavailable here.
export function onMock() {
  pm.response.code = 200;
  pm.response.headers.upsert("Content-Type", "application/json");
  pm.response.setBody({
    ok: true,
    method: pm.request.method,
    path: pm.request.url.getPath()
  });
}
`

// newAddonText is a new addon's text, for its name.
func newAddonText(name string) string {
	return `// What an addon exports, rule scripts and other addons import from "` + name + `";
// request scripts get it with require("` + name + `").
import CryptoJS from "crypto-js";

export function sign(text, key) {
  return CryptoJS.HmacSHA256(text, key).toString();
}
`
}

// checkAddonName adds to a script's name rules that an addon's name is not
// a library require already loads.
func checkAddonName(name string) error {
	if slices.Contains(request.Libraries, name) {
		return fmt.Errorf("require(%q) loads the built-in library", name)
	}
	return capture.CheckScriptName(name)
}

// newScript saves a new script or addon named from base and chooses it. A
// new addon's text is newAddonText's.
func (a *app) newScript(s *scriptStore, base, text string) (string, bool) {
	name := base
	for i := 2; slices.ContainsFunc(s.names, func(n string) bool { return strings.EqualFold(n, name) }); i++ {
		name = fmt.Sprintf("%s%d", base, i)
		if !s.addon {
			name = fmt.Sprintf("%s %d", base, i)
		}
	}
	if s.addon {
		text = newAddonText(name)
	}
	if err := a.writeScript(s, name, text); err != nil {
		a.fail("New script", err)
		return "", false
	}
	s.choice, s.nameFor = name, ""
	return name, true
}

// scriptUsers counts the rules that run a script, or the scripts and
// addons that require an addon by name, and says so.
func (a *app) scriptUsers(s *scriptStore, name string) (int, string) {
	n := 0
	if !s.addon {
		for _, r := range a.rules {
			if (r.Type == capture.RuleScript || r.Type == capture.RuleCodeMock) && r.Target == scriptFile(name) {
				n++
			}
		}
		return n, fmt.Sprintf("Run by %d %s", n, plural(n, "rule", "rules"))
	}
	for _, store := range []*scriptStore{&a.scripts, &a.addons} {
		for other, text := range store.texts {
			if !(store.addon && other == name) && requires(text, name) {
				n++
			}
		}
	}
	for _, r := range a.requests {
		if requires(r.Pre, name) || requires(r.Post, name) {
			n++
		}
	}
	return n, fmt.Sprintf("Required by %d %s", n, plural(n, "script", "scripts"))
}

// requires reports whether src imports or requires the module name, as
// written.
func requires(src, name string) bool {
	for _, form := range []string{`require("%s")`, `require('%s')`, `from "%s"`, `from '%s'`, `import "%s"`, `import '%s'`} {
		if strings.Contains(src, fmt.Sprintf(form, name)) {
			return true
		}
	}
	return false
}

// renameScript renames a script's file and the rules that run it, or an
// addon's file; scripts that require the old name are left to the user.
func (a *app) renameScript(s *scriptStore, old, name string) {
	if name == old {
		return
	}
	check, what := capture.CheckScriptName, "script"
	if s.addon {
		check, what = checkAddonName, "addon"
	}
	err := check(name)
	if err == nil && slices.ContainsFunc(s.names, func(n string) bool { return strings.EqualFold(n, name) && !strings.EqualFold(n, old) }) {
		err = fmt.Errorf("a %s named %q exists", what, name)
	}
	if err != nil {
		a.fail("Rename "+what, err)
		return
	}
	text, st := s.texts[old], s.editors[old]
	if st != nil {
		text = st.Buffer.String()
	}
	// Only the case changes: on a file system that ignores case the old
	// file is the new one, so it must not be removed after the write.
	if strings.EqualFold(old, name) {
		if err := os.Rename(filepath.Join(s.dir, scriptFile(old)), filepath.Join(s.dir, scriptFile(name))); err != nil {
			a.fail("Rename "+what, err)
			return
		}
		a.forgetScript(s, old)
	}
	if err := a.writeScript(s, name, text); err != nil {
		a.fail("Rename "+what, err)
		return
	}
	if !s.addon {
		next, renamed := slices.Clone(a.rules), false
		for i, r := range next {
			if (r.Type == capture.RuleScript || r.Type == capture.RuleCodeMock) && r.Target == scriptFile(old) {
				next[i].Target, renamed = scriptFile(name), true
			}
		}
		if renamed && !a.applyRuleChanges(next) {
			a.fail("Rename script", errors.New(a.ruleUI.problem))
		}
		if d := &a.ruleUI.draft; (d.Type == capture.RuleScript || d.Type == capture.RuleCodeMock) && d.Target == scriptFile(old) {
			d.Target = scriptFile(name)
		}
	}
	if !strings.EqualFold(old, name) {
		a.dropScript(s, old)
	}
	if st != nil {
		st.Label = "Script " + name
		s.editors[name] = st
	}
	s.choice, s.nameFor = name, ""
}

// dropScript removes a script's or addon's file; Script rules skip a missing
// script, Code Mocks return 502, and scripts requiring a missing addon fail.
func (a *app) dropScript(s *scriptStore, name string) {
	path := filepath.Join(s.dir, scriptFile(name))
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.fail("Delete", err)
		return
	}
	a.forgetScript(s, name)
}

// forgetScript drops what the store knows of a file that is gone.
func (a *app) forgetScript(s *scriptStore, name string) {
	delete(a.files, filepath.Join(s.dir, scriptFile(name)))
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
	s := a.shownScripts()
	st := s.editors[s.choice]
	return s.choice, st != nil && st.Buffer.String() != s.texts[s.choice]
}

func (a *app) saveScript(name string) {
	s := a.shownScripts()
	st := s.editors[name]
	if st == nil {
		return
	}
	if err := a.writeScript(s, name, st.Buffer.String()); err != nil {
		a.fail("Save", err)
		return
	}
	switch err := s.problems[name]; {
	case err != nil && s.addon:
		a.warn("Addon saved · it does not parse, so require(\"" + name + "\") fails: " + err.Error())
	case err != nil:
		a.warn("Script saved · it does not parse; Script rules skip it, Code Mocks return 502: " + err.Error())
	case s.addon:
		a.done("Addon saved · scripts load it from their next run")
	default:
		a.done("Script saved · applies to new requests")
	}
}

// openScript shows a rule script on the Scripts page.
func (a *app) openScript(name string) {
	a.addonsShown = false
	a.scripts.choice, a.scripts.nameFor = name, ""
	a.openTools("scripts")
}

// scriptSnippets are what Insert snippet offers for a "request" script, a
// "rule" script or an "addon": modules import where request scripts
// require, and rule scripts also get their hooks' snippets.
func scriptSnippets(kind string) [][2]string {
	shared := [][2]string{
		{"Log a value", "console.log(pm.request.url.toString());\n"},
		{"Read and set a global", "const count = Number(pm.globals.get(\"count\") || 0) + 1;\npm.globals.set(\"count\", count);\n"},
		{"Send a request", "const res = await pm.sendRequest(\"https://example.com/token\");\nconsole.log(res.code, res.json());\n"},
	}
	if kind == "request" {
		return append(shared,
			[2]string{"Hash with crypto-js", "const CryptoJS = require(\"crypto-js\");\nconst signature = CryptoJS.HmacSHA256(pm.request.body.raw, \"secret\").toString(CryptoJS.enc.Hex);\n"},
			[2]string{"Use lodash", "const _ = require(\"lodash\");\nconst ids = _.map(pm.response.json().items, \"id\");\n"})
	}
	// Rule scripts and addons are modules: they import at the top.
	shared = append(shared,
		[2]string{"Import crypto-js", "import CryptoJS from \"crypto-js\";\n"},
		[2]string{"Import lodash", "import _ from \"lodash\";\n"},
		[2]string{"Import an addon", "import { sign } from \"sign\";\n"})
	if kind == "addon" {
		return append(shared, [2]string{"Export a function", "export function sign(text) {\n  return text;\n}\n"})
	}
	return append([][2]string{
		{"Generate a local mock response", newMockScriptText},
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
func snippetMenu(c *ui.Context, st *editor.State, kind string) {
	uikit.MenuSelect(c, "Insert snippet", func(m *ui.Menu) {
		for _, s := range scriptSnippets(kind) {
			if m.Item(s[0]).Chosen() {
				st.Insert(s[1])
			}
		}
	}).Label("Insert snippet")
}

// scriptsPage is the rule scripts or the addons beside the chosen one's
// editor, and for a rule script its Console.
func (a *app) scriptsPage(c *ui.Context, t *ui.Theme, top float32, active bool) {
	s := a.shownScripts()
	if !slices.Contains(s.names, s.choice) {
		s.choice = ""
		if len(s.names) > 0 {
			s.choice = s.names[0]
		}
	}
	what, base, about := "script", "Untitled", "Script rules edit traffic; Code Mock rules answer locally."
	if s.addon {
		what, base, about = "addon", "helpers", "Modules rule scripts import, and request scripts require, by name."
	}
	ui.Column(c).Width(268).MinHeight(0).Padding(top, t.Space(3), t.Space(3), t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Scripts").FontSize(uikit.TextTitle).FontWeight(600).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, "New "+what, "New "+what, plusIcon).Clicked() {
				a.newScript(s, base, newScriptText)
			}
		})
		ui.Row(c).Children(func() {
			shown := 0
			if a.addonsShown {
				shown = 1
			}
			uikit.Segmented(c, &shown, "Rule scripts", "Addons").Label("Show")
			a.addonsShown = shown == 1
		})
		uikit.Meta(c, about)
		if s.problem != nil {
			ui.Text(c, "Read-only: "+s.problem.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger).MaxLines(3)
		}
		a.scriptList(c, t, s, active)
	})
	ui.Divider(c)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(top, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(3)).Children(func() {
		if s.choice == "" {
			title, hint := "No scripts", "Change requests and responses with JavaScript and the pm API, for the traffic a Script rule matches."
			if s.addon {
				title, hint = "No addons", "Code to share between scripts: what an addon exports, rule scripts import from \"its name\" and request scripts require."
			}
			uikit.EmptyState(c, title, hint, func() {
				if uikit.PrimaryButton(c, "New "+what).Clicked() {
					a.newScript(s, base, newScriptText)
				}
			})
			return
		}
		a.scriptEditor(c, t, s, s.choice)
	})
	message := "Rules remain: Script rules skip it; Code Mocks return a local 502 until another script is selected."
	if s.addon {
		message = "Scripts that require it fail until it is back."
	}
	if ui.AlertDialog(c, &s.deleting, "Delete "+what+"?", message, "Cancel", "Delete") == 1 {
		a.dropScript(s, s.choice)
	}
}

// scriptList is the scripts, with their hooks and how many rules run them,
// or the addons, with how many scripts require them.
func (a *app) scriptList(c *ui.Context, t *ui.Theme, s *scriptStore, active bool) {
	choice := slices.Index(s.names, s.choice)
	s.list.Selected = &choice
	s.list.Key = func(i int) any { return s.names[i] }
	s.list.Label = func(i int) string { return s.names[i] }
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	c.SetTheme(&pick)
	ui.List(c, &s.list, len(s.names)).Label(map[bool]string{false: "Scripts", true: "Addons"}[s.addon]).Grow(1).MinHeight(0).Gap(t.Space(0.5)).Rows(func(row ui.ListRow) {
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
				} else if s.addon {
					uikit.Meta(c, `from "`+name+`"`).Font(uikit.FontMono).SingleLine()
				} else {
					uikit.Meta(c, scriptHooks(s, name)).Font(uikit.FontMono).SingleLine()
				}
			})
			if n, tip := a.scriptUsers(s, name); n > 0 {
				uikit.Meta(c, fmt.Sprint(n)).Font(uikit.FontMono).FontFeatures("tnum").Tooltip(tip)
			}
		})
	})
	c.SetTheme(t)
	if choice >= 0 && choice < len(s.names) && s.names[choice] != s.choice {
		s.choice = s.names[choice]
	}
}

func scriptHooks(s *scriptStore, name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var hooks []string
	if p := s.parsed[scriptFile(name)]; p != nil {
		if p.OnRequest {
			hooks = append(hooks, "onRequest")
		}
		if p.OnResponse {
			hooks = append(hooks, "onResponse")
		}
		if p.OnMock {
			hooks = append(hooks, "onMock")
		}
	}
	return cmp.Or(strings.Join(hooks, " · "), "No hooks")
}

// scriptEditor is the chosen script: its name, menu, the code, the Console
// and Save.
func (a *app) scriptEditor(c *ui.Context, t *ui.Theme, s *scriptStore, name string) {
	if s.nameFor != name {
		s.name, s.nameFor = name, name
	}
	st := s.editors[name]
	if st == nil {
		buf := editor.NewBuffer(s.texts[name])
		buf.SetLanguage(editor.LanguageNamed("JavaScript"))
		st = &editor.State{Buffer: buf, NoScrollPastEnd: true, Label: map[bool]string{false: "Script ", true: "Addon "}[s.addon] + name}
		if s.editors == nil {
			s.editors = map[string]*editor.State{}
		}
		s.editors[name] = st
	}
	users, usedBy := a.scriptUsers(s, name)
	what := map[bool]string{false: "Script", true: "Addon"}[s.addon]
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		in := uikit.Input(c, &s.name).Label(what + " name").Grow(1).MaxWidth(320).FontSize(uikit.TextTitle).Height(uikit.HeightLarge)
		if in.Submitted() {
			a.renameScript(s, name, strings.TrimSpace(s.name))
		}
		ui.Spacer(c)
		snippetMenu(c, st, map[bool]string{false: "rule", true: "addon"}[s.addon])
		uikit.MoreMenu(c, what+" actions", func(m *ui.Menu) {
			actions := []action{do("scripts.rename", "Rename", func() { a.renameScript(s, name, strings.TrimSpace(s.name)) }).
				when(strings.TrimSpace(s.name) != name, "Type the new name first").in("edit")}
			if !s.addon {
				actions = append(actions, do("scripts.rule", "New rule running it", func() {
					a.editRule(-1, capture.Rule{Name: name, Type: capture.RuleScript, Target: scriptFile(name)})
					a.openTools("rules")
				}).in("edit"), do("scripts.mock-rule", "New Code Mock rule", func() {
					a.editRule(-1, capture.Rule{Name: name, Type: capture.RuleCodeMock, Target: scriptFile(name)})
					a.openTools("mocks")
				}).in("edit"))
			}
			actionMenu(m, append(actions, do("scripts.delete", "Delete…", func() { s.deleting = true }).in("danger")))
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
	if !s.addon {
		a.scriptConsole(c, t, name)
	}
	_, modified := a.scriptToSave()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		note := "No rule runs it yet; add a Script or Code Mock rule."
		switch {
		case s.addon && users > 0:
			note = usedBy + ` · import it from "` + name + `", or require it.`
		case s.addon:
			note = `Import it in a rule script from "` + name + `", or require it in a request script.`
		case users > 0:
			note = usedBy + " · changes apply to new requests."
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
