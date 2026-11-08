package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// mcpConsentState is the pending confirmation for one stdio command. It
// holds the expanded plan only in memory; the modal shows command, arguments,
// working directory and environment names, never environment values.
type mcpConsentState struct {
	open bool
	d    *requestDocument
	plan request.MCPPlan
	key  string
}

type mcpConsentFile struct {
	Format   string          `json:"format"`
	Version  int             `json:"version"`
	Consents map[string]bool `json:"consents"`
}

const mcpConsentFormat = "vscroxy-mcp-consents"

// loadMCPConsents reads the local approval hashes. A missing file is an empty
// list; a damaged file is ignored so it cannot block the app.
func loadMCPConsents(path string) (map[string]bool, error) {
	if path == "" {
		return map[string]bool{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return map[string]bool{}, err
	}
	var file mcpConsentFile
	if err := json.Unmarshal(data, &file); err != nil || file.Format != mcpConsentFormat || file.Version != 1 {
		return map[string]bool{}, fmt.Errorf("invalid MCP consent file")
	}
	out := make(map[string]bool, len(file.Consents))
	for key, approved := range file.Consents {
		if approved && key != "" {
			out[key] = true
		}
	}
	return out, nil
}

func saveMCPConsents(path string, consents map[string]bool) error {
	if path == "" {
		return nil
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for key, approved := range consents {
			if approved && !yield(key) {
				return
			}
		}
	})
	if len(keys) > 4096 {
		keys = keys[:4096]
	}
	file := mcpConsentFile{Format: mcpConsentFormat, Version: 1, Consents: make(map[string]bool, len(keys))}
	for _, key := range keys {
		file.Consents[key] = true
	}
	data, err := json.Marshal(file, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return store.WriteFile(path, data)
}

// mcpConsentKey binds an approval to the document and the exact expanded
// command shape. Environment values are deliberately excluded: only their
// names are shown, and a value change should not ask again.
func mcpConsentKey(d *requestDocument, plan request.MCPPlan) string {
	var b strings.Builder
	identity := "document"
	if d != nil {
		switch {
		case d.assetID != "":
			identity = "asset:" + d.assetID
		case d.debugKey != "":
			identity = "debug:" + d.debugKey
		case d.id != 0:
			identity = fmt.Sprintf("doc:%d", d.id)
		}
	}
	b.WriteString(identity)
	b.WriteByte(0)
	b.WriteString(plan.Command)
	b.WriteByte(0)
	for _, arg := range plan.Args {
		b.WriteString(arg)
		b.WriteByte(0)
	}
	b.WriteString(plan.WorkingDir)
	b.WriteByte(0)
	names := make([]string, 0, len(plan.Env))
	for _, entry := range plan.Env {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func (a *app) approveMCPConsent() {
	s := &a.mcpConsent
	if !s.open || s.d == nil || s.key == "" {
		return
	}
	if a.mcpConsents == nil {
		a.mcpConsents = map[string]bool{}
	}
	a.mcpConsents[s.key] = true
	if err := saveMCPConsents(a.mcpConsentsPath, a.mcpConsents); err != nil {
		a.warn("MCP approval could not be saved; it will ask again next time.")
	}
	s.open = false
	d, plan := s.d, s.plan
	s.d, s.plan, s.key = nil, request.MCPPlan{}, ""
	a.startMCP(d, plan)
}

func (a *app) mcpConsentModal(c *ui.Context, t *ui.Theme) {
	s := &a.mcpConsent
	if !s.open || s.d == nil {
		return
	}
	plan := s.plan
	uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Run local MCP server?")
			uikit.Hint(c, "This starts a local command with your user account. Only continue for a server you trust.")
			uikit.Meta(c, "Command").Padding(0, t.Space(1))
			ui.Text(c, plan.Command).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable()
			if len(plan.Args) > 0 {
				uikit.Meta(c, "Arguments").Padding(0, t.Space(1))
				ui.Text(c, strings.Join(plan.Args, "\n")).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable()
			}
			dir := plan.WorkingDir
			if dir == "" {
				dir = "(the app's working directory)"
			}
			uikit.Meta(c, "Working directory").Padding(0, t.Space(1))
			ui.Text(c, dir).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable()
			uikit.Meta(c, "Environment variables · names only; values stay hidden").Padding(0, t.Space(1))
			names := make([]string, 0, len(plan.Env))
			for _, entry := range plan.Env {
				name, _, _ := strings.Cut(entry, "=")
				if name != "" {
					names = append(names, name)
				}
			}
			slices.Sort(names)
			if len(names) == 0 {
				names = []string{"(none)"}
			}
			ui.Text(c, strings.Join(names, ", ")).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable()
			ui.Spacer(c).Height(t.Space(2))
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, "Start server").Clicked() {
					a.approveMCPConsent()
				}
			})
		})
	})
	if !s.open {
		s.d, s.plan, s.key = nil, request.MCPPlan{}, ""
	}
}
