package app

// Import and export of the configuration, for backups and sharing: one JSON
// file with the settings and lists, the rules with the files they answer
// from, the rule scripts and addons, the Host profiles and the breakpoint templates.
// Secrets (the vault) and variables are not part of it.

import (
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const configFormat = "vscroxy-config"

// configBundle is an exported configuration. A Map Local rule's file is in
// Mocks, and its Target is "mocks/" and the name it has there.
type configBundle struct {
	Format   string    `json:"format"`
	Version  int       `json:"version"`
	Exported time.Time `json:"exported"`

	Settings *settings          `json:"settings,omitempty"`
	SSL      *capture.SSLConfig `json:"ssl,omitempty"`
	// Lists are the ignore and allow lists, by file name.
	Lists map[string][]string `json:"lists,omitempty"`

	Rules     []capture.Rule    `json:"rules,omitempty"`
	Mocks     map[string][]byte `json:"mocks,omitempty"`
	Scripts   map[string]string `json:"scripts,omitempty"`
	Addons    map[string]string `json:"addons,omitempty"`
	Templates []bpTemplate      `json:"breakpointTemplates,omitempty"`

	Hosts     *hosts.Set        `json:"hosts,omitempty"`
	HostsText map[string]string `json:"hostsText,omitempty"`
}

// The parts of a bundle an import can replace.
const (
	partRules = iota
	partHosts
	partSettings
)

// configImport is a bundle read from a file, waiting for the user to choose
// what it replaces.
type configImport struct {
	open   bool
	path   string
	bundle configBundle
	parts  [3]bool
}

// currentSettings are the settings as they are now.
func (a *app) currentSettings() settings {
	return settings{
		Appearance:        a.appearance,
		SidebarWidth:      a.sidebarWidth,
		TablePercent:      a.tablePercent,
		InspectorLayout:   a.workbench.layout,
		SideTablePercent:  a.workbench.sidePercent,
		SidebarHidden:     a.workbench.sidebarHidden,
		Columns:           savedLayout(a.columns),
		CustomColumns:     a.customColumns,
		HiddenColumns:     a.hiddenColumns,
		KeyLogPath:        a.keyLogPath,
		ProxyPort:         a.proxyPort,
		VaultScripts:      a.vault.scripts,
		CookiePersistence: a.cookiePersistence,
		Guide:             a.guide,
		AllowList:         a.allowOn,
		FlowLimit:         a.flowLimit,
		MemoryLimit:       a.memoryLimit,
		DiskLimit:         a.diskLimit,
	}
}

// configLists are the list files a bundle carries and the text each holds.
func (a *app) configLists() map[string]*string {
	return map[string]*string{
		a.ignorePath: &a.ignoreText, a.ignoreAppsPath: &a.ignoreAppsText,
		a.allowPath: &a.allowText, a.allowAppsPath: &a.allowAppsText,
	}
}

// exportConfig gathers the configuration. notes say what it could not take
// along: folders and files a Map Local rule answers from that are not there
// or too large stay paths on this computer.
func (a *app) exportConfig() (b configBundle, notes []string) {
	s := a.currentSettings()
	// The key log, setup guide and cookie retention are this computer's:
	// a configuration file never turns cookie persistence on elsewhere.
	s.KeyLogPath, s.Guide, s.CookiePersistence = "", false, false
	ssl := a.inspectionConfig()
	b = configBundle{Format: configFormat, Version: 1, Exported: time.Now().UTC(), Settings: &s, SSL: &ssl,
		Lists: map[string][]string{}, Rules: cloneRuleList(a.rules), Mocks: map[string][]byte{}, Scripts: map[string]string{},
		Templates: slices.Clone(a.bpTemplates)}
	for path, text := range a.configLists() {
		if path != "" {
			b.Lists[filepath.Base(path)] = splitHosts(*text)
		}
	}
	packed := map[string]string{} // file → its name in Mocks
	kept := 0
	for i, r := range b.Rules {
		if r.Type != capture.RuleMapLocal {
			continue
		}
		if name, ok := packed[r.Target]; ok {
			b.Rules[i].Target = "mocks/" + name
			continue
		}
		info, err := os.Stat(r.Target)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			kept++
			continue
		}
		data, err := os.ReadFile(r.Target)
		if err != nil {
			kept++
			continue
		}
		name := filepath.Base(r.Target)
		for n := 2; b.Mocks[name] != nil; n++ {
			ext := filepath.Ext(r.Target)
			name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(filepath.Base(r.Target), ext), n, ext)
		}
		b.Mocks[name], packed[r.Target] = data, name
		b.Rules[i].Target = "mocks/" + name
	}
	if kept > 0 {
		notes = append(notes, fmt.Sprintf("%d Map Local %s a folder or a missing or large file, kept as a path", kept, plural(kept, "rule answers from", "rules answer from")))
	}
	for _, name := range a.scripts.names {
		b.Scripts[name] = a.scripts.texts[name]
	}
	if len(a.addons.names) > 0 {
		b.Addons = map[string]string{}
		for _, name := range a.addons.names {
			b.Addons[name] = a.addons.texts[name]
		}
	}
	if a.profiles.problem == nil && len(a.profiles.set.Profiles) > 0 {
		set := a.profiles.set
		set.Profiles = slices.Clone(set.Profiles)
		b.Hosts, b.HostsText = &set, map[string]string{}
		for _, p := range set.Profiles {
			b.HostsText[p.Name] = p.Text
		}
	}
	return b, notes
}

func cloneRuleList(list []capture.Rule) []capture.Rule {
	out := slices.Clone(list)
	for i := range out {
		out[i].Headers = list[i].Headers.Clone()
		out[i].Apps, out[i].When = slices.Clone(list[i].Apps), slices.Clone(list[i].When)
		if list[i].Network != nil {
			out[i].Network = new(*list[i].Network)
		}
	}
	return out
}

// parseConfig reads an exported configuration and checks what an import
// would write: the rules, the scripts' and profiles' names.
func parseConfig(data []byte) (configBundle, error) {
	var b configBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("not a vscroxy configuration: %w", err)
	}
	if b.Format != configFormat {
		return b, errors.New("not a vscroxy configuration")
	}
	if b.Version != 1 {
		return b, fmt.Errorf("configuration version %d is not supported; update vscroxy", b.Version)
	}
	if err := capture.ValidateRules(b.Rules); err != nil {
		return b, err
	}
	for _, r := range b.Rules {
		if name, ok := strings.CutPrefix(r.Target, "mocks/"); ok && r.Type == capture.RuleMapLocal {
			if _, ok := b.Mocks[name]; !ok {
				return b, fmt.Errorf("rule %q answers from %s, which the file does not include", r.Name, name)
			}
		}
	}
	for name := range b.Mocks {
		if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			return b, fmt.Errorf("mock file name %q is not a plain name", name)
		}
	}
	for name := range b.Scripts {
		if err := capture.CheckScriptName(name); err != nil {
			return b, fmt.Errorf("script %q: %w", name, err)
		}
	}
	for name := range b.Addons {
		if err := checkAddonName(name); err != nil {
			return b, fmt.Errorf("addon %q: %w", name, err)
		}
	}
	if b.Hosts != nil {
		for i, p := range b.Hosts.Profiles {
			if err := b.Hosts.CheckName(p.Name, i); err != nil {
				return b, fmt.Errorf("Host profile %d: %w", i+1, err)
			}
		}
	}
	return b, nil
}

// exportConfigTo writes the configuration to path.
func (a *app) exportConfigTo(path string) {
	b, notes := a.exportConfig()
	data, err := json.Marshal(b, jsontext.WithIndent("  "))
	if err == nil {
		err = store.WriteFile(path, append(data, '\n'))
	}
	if err != nil {
		a.fail("Export configuration", err)
		return
	}
	text := fmt.Sprintf("Exported %d %s, %d %s and %d Host %s to %s", len(b.Rules), plural(len(b.Rules), "rule", "rules"),
		len(b.Scripts), plural(len(b.Scripts), "script", "scripts"), len(a.profiles.set.Profiles), plural(len(a.profiles.set.Profiles), "profile", "profiles"), filepath.Base(path))
	if len(notes) > 0 {
		a.warn(text + " · " + strings.Join(notes, "; "))
		return
	}
	a.done(text)
}

// importConfig replaces the chosen parts of the configuration with b's. It
// writes the files, then reads them all again, as Reload configuration does.
func (a *app) importConfig(b configBundle, parts [3]bool) error {
	if parts[partRules] {
		if a.rulesPath == "" {
			return errors.New("rules storage is unavailable")
		}
		rules := cloneRuleList(b.Rules)
		written := map[string]string{}
		var made []capture.Rule
		for i, r := range rules {
			name, ok := strings.CutPrefix(r.Target, "mocks/")
			if r.Type != capture.RuleMapLocal || !ok {
				continue
			}
			if path, ok := written[name]; ok {
				rules[i].Target = path
				continue
			}
			path := filepath.Join(a.mocksDir(), rand.Text()+filepath.Ext(name))
			if err := store.WriteFile(path, b.Mocks[name]); err != nil {
				removeMocks(made)
				return err
			}
			rules[i].Target, written[name] = path, path
			made = append(made, rules[i])
		}
		if err := a.writeRules(rules); err != nil {
			removeMocks(made)
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(b.Scripts)) {
			if err := a.writeScript(&a.scripts, name, b.Scripts[name]); err != nil {
				return err
			}
		}
		for _, name := range slices.Sorted(maps.Keys(b.Addons)) {
			if err := a.writeScript(&a.addons, name, b.Addons[name]); err != nil {
				return err
			}
		}
		if a.bpTemplatesPath != "" {
			if err := a.writeBreakTemplates(slices.Clone(b.Templates)); err != nil {
				return err
			}
		}
		a.ruleUI.open = false
	}
	if parts[partHosts] && b.Hosts != nil && a.profiles.dir != "" {
		set := *b.Hosts
		set.Profiles = slices.Clone(set.Profiles)
		for _, p := range set.Profiles {
			if err := a.file(a.profiles.path(p.Name)).Write([]byte(b.HostsText[p.Name])); err != nil {
				return err
			}
		}
		for _, p := range a.profiles.set.Profiles {
			if set.Index(p.Name) < 0 {
				_ = os.Remove(a.profiles.path(p.Name))
				delete(a.files, a.profiles.path(p.Name))
			}
		}
		data, err := set.MarshalIndex()
		if err == nil {
			err = a.file(filepath.Join(a.profiles.dir, hosts.IndexFile)).Write(data)
		}
		if err != nil {
			return err
		}
		a.profiles.editors = nil
	}
	if parts[partSettings] {
		if b.SSL != nil && a.sslPath != "" && !a.saveSSL(*b.SSL) {
			return errors.New("HTTPS settings were not saved")
		}
		for path := range a.configLists() {
			if lines, ok := b.Lists[filepath.Base(path)]; ok && path != "" {
				if err := a.writeList(path, lines); err != nil {
					return err
				}
			}
		}
		if b.Settings != nil {
			a.applySettings(*b.Settings)
		}
	}
	a.reloadConfig()
	return nil
}

// applySettings uses imported settings, keeping this computer's key log,
// setup guide and cookie retention, and saves them.
func (a *app) applySettings(s settings) {
	a.appearance = s.Appearance
	if s.SidebarWidth > 0 {
		a.sidebarWidth = s.SidebarWidth
	}
	if s.TablePercent > 0 {
		a.tablePercent = s.TablePercent
	}
	a.workbench.layout = inspectorLayout(s.InspectorLayout)
	a.workbench.sidebarHidden = s.SidebarHidden
	if s.SideTablePercent > 0 {
		a.workbench.sidePercent = s.SideTablePercent
	}
	a.columns = s.Columns.layout()
	a.customColumns, a.hiddenColumns = slices.Clone(s.CustomColumns), slices.Clone(s.HiddenColumns)
	a.proxyPort, a.vault.scripts, a.allowOn = s.ProxyPort, s.VaultScripts, s.AllowList
	a.flowLimit, a.memoryLimit, a.diskLimit = s.FlowLimit, s.MemoryLimit, s.DiskLimit
	a.proxyPortInput = ""
	if s.ProxyPort != 0 {
		a.proxyPortInput = fmt.Sprint(s.ProxyPort)
	}
	a.lastSave = time.Time{}
	_ = a.persistSettings()
}

// chooseConfigExport asks where to save the configuration.
func (a *app) chooseConfigExport() {
	win := a.win
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "Export configuration", DefaultPath: "vscroxy-config.json",
			Filters: []mygo.FileFilter{{Name: "vscroxy configuration", Extensions: []string{"json"}}}})
		win.Update(func() {
			if err != nil {
				a.fail("Export configuration", err)
			} else if path != "" {
				a.exportConfigTo(path)
			}
		})
	}()
}

// chooseConfigImport asks for a configuration file and shows what it holds.
func (a *app) chooseConfigImport() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Import configuration",
			Filters: []mygo.FileFilter{{Name: "vscroxy configuration", Extensions: []string{"json"}}}})
		var data []byte
		if err == nil && len(paths) > 0 {
			data, err = os.ReadFile(paths[0])
		}
		win.Update(func() {
			if err != nil {
				a.fail("Import configuration", err)
			} else if len(paths) > 0 {
				a.readConfigImport(paths[0], data)
			}
		})
	}()
}

// readConfigImport opens the import dialog on data, or says why it cannot.
func (a *app) readConfigImport(path string, data []byte) {
	b, err := parseConfig(data)
	if err != nil {
		a.fail("Import configuration", err)
		return
	}
	a.configImport = configImport{open: true, path: path, bundle: b}
	a.configImport.parts = [3]bool{len(b.Rules)+len(b.Scripts)+len(b.Addons)+len(b.Templates) > 0, b.Hosts != nil, b.Settings != nil}
}

// configImportDialog lists what the file holds; the parts chosen replace
// what this computer has.
func (a *app) configImportDialog(c *ui.Context, t *ui.Theme) {
	in := &a.configImport
	if !in.open {
		return
	}
	b := in.bundle
	uikit.Modal(c, &in.open, func() {
		uikit.DialogColumn(c, 520).Children(func() {
			uikit.DialogTitle(c, "Import configuration")
			uikit.Meta(c, fmt.Sprintf("%s · exported %s", filepath.Base(in.path), b.Exported.Local().Format("2006-01-02 15:04")))
			uikit.Prose(c, "The parts you choose replace what vscroxy has now. Secrets and variables are never part of a configuration file.")
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				part := func(i int, available bool, label, detail string) {
					ui.Column(c).Gap(t.Space(0.5)).Disabled(!available).Children(func() {
						ui.Checkbox(c, &in.parts[i], label)
						uikit.Meta(c, detail).Padding(0, 0, 0, t.Space(6))
					})
					if !available {
						in.parts[i] = false
					}
				}
				part(partRules, len(b.Rules)+len(b.Scripts)+len(b.Addons)+len(b.Templates) > 0, "Rules, mocks and scripts",
					fmt.Sprintf("%d %s, %d mock %s, %d %s, %d %s, %d breakpoint %s. Scripts and addons with the same name are replaced.",
						len(b.Rules), plural(len(b.Rules), "rule", "rules"), len(b.Mocks), plural(len(b.Mocks), "file", "files"),
						len(b.Scripts), plural(len(b.Scripts), "script", "scripts"), len(b.Addons), plural(len(b.Addons), "addon", "addons"), len(b.Templates), plural(len(b.Templates), "template", "templates")))
				profiles := 0
				if b.Hosts != nil {
					profiles = len(b.Hosts.Profiles)
				}
				part(partHosts, b.Hosts != nil, "Host profiles", fmt.Sprintf("%d %s, with their switches and order.", profiles, plural(profiles, "profile", "profiles")))
				part(partSettings, b.Settings != nil, "Settings and lists", "Appearance, layout, proxy port, HTTPS decryption, and the ignore and allow lists.")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					in.open = false
				}
				ui.Row(c).Disabled(in.parts == [3]bool{}).Children(func() {
					if uikit.PrimaryButton(c, "Replace").Clicked() {
						if err := a.importConfig(b, in.parts); err != nil {
							a.fail("Import configuration", err)
						} else {
							a.done("Imported " + filepath.Base(in.path))
						}
						in.open = false
					}
				})
			})
		})
	})
}
