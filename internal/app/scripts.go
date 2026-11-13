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
	"github.com/baba983/vscroxy/locales"
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
func (s *scriptStore) Hooks(file string) (bool, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.parsed[file]; p != nil {
		return p.OnRequest, p.OnResponse, p.OnMessage
	}
	return false, false, false
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
			a.fail(locales.ScriptUnreadable(name), err)
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
// import works for lodash, crypto-js, moment, chai, cheerio, xml2js, csv-parse, ajv, uuid and your addons.

// Change pm.request before it goes upstream:
export function onRequest() {
  pm.request.headers.upsert("X-Debug", "1");
}

// Change pm.response before the client gets it:
export function onResponse() {
  // if (pm.response.code === 500) pm.response.code = 200;
}

// Complete WebSocket data messages (not HTTP or control frames):
export function onMessage(message) {
  // if (message.direction === "out" && message.type === "text") message.setText(message.text());
  // Binary bytes: message.base64() / message.setBase64("/wA="). Drop just this message: message.drop().
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
		a.fail(locales.NewScript(), err)
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
		a.fail(locales.RenameThing(what), err)
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
			a.fail(locales.RenameThing(what), err)
			return
		}
		a.forgetScript(s, old)
	}
	if err := a.writeScript(s, name, text); err != nil {
		a.fail(locales.RenameThing(what), err)
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
			a.fail(locales.RenameScript(), errors.New(a.ruleUI.problem))
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
		a.fail(locales.Delete(), err)
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
		a.fail(locales.Save(), err)
		return
	}
	switch err := s.problems[name]; {
	case err != nil && s.addon:
		a.warn("Addon saved · it does not parse, so require(\"" + name + "\") fails: " + err.Error())
	case err != nil:
		a.warn(locales.ScriptSavedNotParse(err.Error()))
	case s.addon:
		a.done(locales.AddonSavedScriptsLoadIt())
	default:
		a.done(locales.ScriptSavedAppliesToNew())
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
		{locales.SnippetLogValue(), "console.log(pm.request.url.toString());\n$0"},
		{locales.SnippetReadGlobal(), "const count = Number(pm.globals.get(\"${1:count}\") || 0) + 1;\npm.globals.set(\"${1:count}\", count);\n$0"},
		{locales.SnippetSendRequest(), "const res = await pm.sendRequest(\"${1:https://example.com/token}\");\nconsole.log(res.code, res.json());\n$0"},
	}
	if kind == "request" {
		return append(shared,
			[2]string{locales.SnippetHashCryptoJS(), "const CryptoJS = require(\"crypto-js\");\nconst signature = CryptoJS.HmacSHA256(pm.request.body.raw, \"${1:secret}\").toString(CryptoJS.enc.Hex);\n$0"},
			[2]string{locales.SnippetUseLodash(), "const _ = require(\"lodash\");\nconst ids = _.map(pm.response.json().items, \"${1:id}\");\n$0"},
			[2]string{locales.SnippetFormatDateMoment(), "const moment = require(\"moment\");\nconst at = moment(\"${1:2024-01-31T23:30:45Z}\").format(\"${2:YYYY-MM-DD HH:mm:ss Z}\");\n$0"},
			[2]string{locales.SnippetParseHtmlCheerio(), "const cheerio = require(\"cheerio\");\nconst $ = cheerio.load(pm.response.text());\nconsole.log($(\"title\").text());\n$0"},
			[2]string{locales.SnippetParseXmlXml2js(), "const xml2js = require(\"xml2js\");\nxml2js.parseString(pm.response.text(), { explicitArray: false }, (err, result) => {\n  if (err) throw err;\n  console.log(result);\n});\n$0"},
			[2]string{locales.SnippetParseCsv(), "const parse = require(\"csv-parse/lib/sync\");\nconst rows = parse(pm.response.text(), { columns: true });\nconsole.log(rows);\n$0"},
			[2]string{locales.SnippetValidateAjv(), "const Ajv = require(\"ajv\");\nconst valid = new Ajv().validate(${1:schema}, pm.response.json());\npm.test(\"schema\", () => pm.expect(valid).to.be.true);\n$0"},
			[2]string{locales.SnippetGenerateUuid(), "const uuid = require(\"uuid\");\npm.variables.set(\"${1:id}\", uuid.v4());\n$0"},
			[2]string{locales.SnippetDecodeBase64(), "const { Buffer } = require(\"buffer\");\nconst text = Buffer.from(pm.response.text(), \"base64\").toString();\nconsole.log(text);\n$0"})
	}
	// Rule scripts and addons are modules: they import at the top.
	shared = append(shared,
		[2]string{locales.SnippetImportCryptoJS(), "import CryptoJS from \"crypto-js\";\n$0"},
		[2]string{locales.SnippetImportLodash(), "import _ from \"lodash\";\n$0"},
		[2]string{locales.SnippetImportMoment(), "import moment from \"moment\";\n$0"},
		[2]string{locales.SnippetImportCheerio(), "import cheerio from \"cheerio\";\n$0"},
		[2]string{locales.SnippetImportXml2js(), "import xml2js from \"xml2js\";\n$0"},
		[2]string{locales.SnippetImportUuid(), "import { v4 as uuidv4 } from \"uuid\";\n$0"},
		[2]string{locales.SnippetImportAddon(), "import { ${1:sign} } from \"${1:sign}\";\n$0"})
	if kind == "addon" {
		return append(shared, [2]string{locales.SnippetExportFunction(), "export function ${1:sign}(text) {\n  return text;\n}\n$0"})
	}
	return append([][2]string{
		{"Generate a local mock response", newMockScriptText},
		{locales.SnippetAddRequestHeader(), "pm.request.headers.upsert(\"${1:X-Debug}\", \"${2:1}\");\n$0"},
		{locales.SnippetRewriteUrl(), "pm.request.url = pm.request.url.toString().replace(\"${1:/v1/}\", \"${2:/v2/}\");\n$0"},
		{locales.SnippetAddQueryParameter(), "pm.request.url.query.upsert(\"${1:debug}\", \"${2:1}\");\n$0"},
		{locales.SnippetReadEnv(), "const token = process.env.${1:API_TOKEN};\nif (token) pm.request.headers.upsert(\"Authorization\", \"Bearer \" + token);\n$0"},
		{locales.SnippetChangeStatus(), "if (pm.response.code >= ${1:500}) pm.response.code = ${2:200};\n$0"},
		{locales.SnippetEditJson(), "const body = pm.response.json();\nbody.${1:debug} = true;\npm.response.setBody(body);\n$0"},
		{locales.SnippetSetResponseHeader(), "pm.response.headers.upsert(\"${1:Access-Control-Allow-Origin}\", \"${2:*}\");\n$0"},
		{locales.SnippetPassValue(), "// In onRequest:\npm.variables.set(\"${1:startedAt}\", Date.now());\n// In onResponse:\nconsole.log(\"took\", Date.now() - Number(pm.variables.get(\"${1:startedAt}\")), \"ms\");\n$0"},
	}, shared...)
}

// snippetMenu inserts a snippet at the caret of st.
func snippetMenu(c *ui.Context, st *editor.State, kind string) {
	uikit.MenuSelect(c, locales.InsertSnippet(), func(m *ui.Menu) {
		for _, s := range scriptSnippets(kind) {
			if m.Item(s[0]).Chosen() {
				// The templates are parsed once at the click: a bad one
				// leaves the editor alone rather than inserting its syntax.
				if snippet, err := editor.ParseSnippet(s[1]); err == nil {
					st.InsertSnippet(snippet)
				}
			}
		}
	}).Label(locales.InsertSnippet(

	// scriptsPage is the rule scripts or the addons beside the chosen one's
	// editor, and for a rule script its Console.
	))
}

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
			ui.Text(c, locales.Scripts()).FontSize(uikit.TextTitle).FontWeight(500).TextColor(t.Text).Grow(1)
			if uikit.IconButton(c, "New "+what, "New "+what, plusIcon).Clicked() {
				a.newScript(s, base, newScriptText)
			}
		})
		ui.Row(c).Children(func() {
			shown := 0
			if a.addonsShown {
				shown = 1
			}
			uikit.Segmented(c, &shown, locales.RuleScripts(), locales.Addons()).Label(locales.Show())
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
				uikit.Tip(c, uikit.Meta(c, fmt.Sprint(n)).Font(uikit.FontMono).FontFeatures("tnum"), tip)
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
		if p.OnMessage {
			hooks = append(hooks, "onMessage")
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
			actions := []action{do("scripts.rename", locales.Rename(), func() { a.renameScript(s, name, strings.TrimSpace(s.name)) }).
				when(strings.TrimSpace(s.name) != name, "Type the new name first").in("edit")}
			if !s.addon {
				actions = append(actions, do("scripts.rule", locales.NewRuleRunningIt(), func() {
					a.editRule(-1, capture.Rule{Name: name, Type: capture.RuleScript, Target: scriptFile(name)})
					a.openTools("rules")
				}).in("edit"), do("scripts.mock-rule", locales.NewCodeMockRule(), func() {
					a.editRule(-1, capture.Rule{Name: name, Type: capture.RuleCodeMock, Target: scriptFile(name)})
					a.openTools("mocks")
				}).in("edit"))
			}
			actionMenu(m, append(actions, do("scripts.delete", locales.Delete2(), func() { s.deleting = true }).in("danger")))
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
			if uikit.Tip(c, uikit.PrimaryButton(c, locales.Save()), locales.SaveCtrlCmdS()).Clicked() {
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
		ui.Row(c).Height(uikit.HeightControl+4).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Label(c, locales.Console()).Grow(1)
			if uikit.CompactButton(c, locales.Clear()).Disabled(len(lines) == 0).Clicked() {
				s.mu.Lock()
				s.console = slices.DeleteFunc(s.console, func(l scriptLog) bool { return l.script == name })
				s.mu.Unlock()
			}
		})
		ui.Divider(c)
		ui.Scroll(c.Key("script-console-"+name)).Grow(1).FillWidth().Padding(t.Space(1), t.Space(2)).Gap(t.Space(1)).Children(func() {
			if len(lines) == 0 {
				uikit.Hint(c, locales.ConsoleLogAndErrorsShow())
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
