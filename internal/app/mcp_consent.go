package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// mcpConsentState is the pending confirmation for one stdio command. It
// holds the expanded plan only in memory; the modal shows command, arguments,
// working directory and environment names, never environment values.
type mcpConsentState struct {
	open          bool
	d             *requestDocument
	plan          request.MCPPlan
	shape, full   string
	valuesChanged bool
	changed       []string
}

// mcpConsentApproval is one approved hash. Env holds HMACs of environment
// values, never the values themselves; it lets the dialog name a changed
// variable without making the consent file a dictionary for Vault secrets.
type mcpConsentApproval struct {
	LastUsed int64             `json:"lastUsed"`
	Persist  bool              `json:"persist,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type mcpConsentFile struct {
	Format    string                        `json:"format"`
	Version   int                           `json:"version"`
	Approvals map[string]mcpConsentApproval `json:"approvals"`
}

const (
	mcpConsentFormat  = "vscroxy-mcp-consents"
	mcpConsentVersion = 2
	mcpConsentLimit   = 4096
	mcpConsentKeySize = 32
)

// loadMCPConsents reads the local approval hashes. A missing file is an empty
// list; a damaged file is ignored so it cannot block the app. The old v1
// format has no shape/full distinction and is discarded by design.
func loadMCPConsents(path string) (map[string]mcpConsentApproval, error) {
	if path == "" {
		return map[string]mcpConsentApproval{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]mcpConsentApproval{}, nil
		}
		return map[string]mcpConsentApproval{}, err
	}
	var file mcpConsentFile
	if err := json.Unmarshal(data, &file); err != nil || file.Format != mcpConsentFormat || file.Version != mcpConsentVersion {
		return map[string]mcpConsentApproval{}, fmt.Errorf("invalid MCP consent file")
	}
	out := make(map[string]mcpConsentApproval, len(file.Approvals))
	for key, approval := range file.Approvals {
		if key != "" {
			approval.Persist = true
			out[key] = approval
		}
	}
	return pruneMCPConsents(out), nil
}

func saveMCPConsents(path string, consents map[string]mcpConsentApproval) error {
	if path == "" {
		return nil
	}
	out := make(map[string]mcpConsentApproval, len(consents))
	for key, approval := range consents {
		if key != "" && approval.Persist {
			out[key] = approval
		}
	}
	out = pruneMCPConsents(out)
	file := mcpConsentFile{Format: mcpConsentFormat, Version: mcpConsentVersion, Approvals: out}
	data, err := json.Marshal(file, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return store.WriteFile(path, data)
}

// pruneMCPConsents keeps the most recently used approvals. The cap applies
// to shape and full hashes together.
func pruneMCPConsents(consents map[string]mcpConsentApproval) map[string]mcpConsentApproval {
	if len(consents) <= mcpConsentLimit {
		return consents
	}
	keys := make([]string, 0, len(consents))
	for key := range consents {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		left, right := consents[a].LastUsed, consents[b].LastUsed
		switch {
		case left > right:
			return -1
		case left < right:
			return 1
		default:
			return strings.Compare(a, b)
		}
	})
	out := make(map[string]mcpConsentApproval, mcpConsentLimit)
	for _, key := range keys[:mcpConsentLimit] {
		out[key] = consents[key]
	}
	return out
}

// mcpConsentKey binds a shape approval to the document and the exact expanded
// command shape. Environment values are deliberately excluded; the full key
// below includes them.
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

// mcpConsentFullKey includes every expanded environment value. The HMAC key
// lives in a separate 0600 file, so the consent file cannot be used to guess
// low-entropy Vault values offline.
func mcpConsentFullKey(shape string, plan request.MCPPlan, key []byte) string {
	entries := append([]string(nil), plan.Env...)
	slices.Sort(entries)
	mac := hmac.New(sha256.New, key)
	io.WriteString(mac, "full\x00")
	io.WriteString(mac, shape)
	for _, entry := range entries {
		io.WriteString(mac, "\x00")
		io.WriteString(mac, entry)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func mcpConsentEnvHashes(plan request.MCPPlan, key []byte) map[string]string {
	out := make(map[string]string, len(plan.Env))
	for _, entry := range plan.Env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		mac := hmac.New(sha256.New, key)
		io.WriteString(mac, "env\x00")
		io.WriteString(mac, name)
		io.WriteString(mac, "\x00")
		io.WriteString(mac, value)
		out[name] = hex.EncodeToString(mac.Sum(nil))
	}
	return out
}

func mcpChangedEnvNames(previous map[string]string, plan request.MCPPlan, key []byte) []string {
	current := mcpConsentEnvHashes(plan, key)
	names := make([]string, 0, len(previous)+len(current))
	for name := range previous {
		names = append(names, name)
	}
	for name := range current {
		if _, ok := previous[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	var changed []string
	for _, name := range names {
		if previous[name] != current[name] {
			changed = append(changed, name)
		}
	}
	return changed
}

// loadOrCreateMCPConsentKey returns the HMAC key, creating it privately on
// first use. A malformed key is replaced; old approvals then simply ask again.
func loadOrCreateMCPConsentKey(path string) ([]byte, error) {
	if path == "" {
		return newMCPConsentKey(), nil
	}
	data, err := os.ReadFile(path)
	if err == nil && len(data) == mcpConsentKeySize {
		_ = os.Chmod(path, 0o600)
		return data, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return newMCPConsentKey(), err
	}
	key := newMCPConsentKey()
	if err := store.WriteFile(path, key); err != nil {
		return key, err
	}
	return key, nil
}

func newMCPConsentKey() []byte {
	key := make([]byte, mcpConsentKeySize)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand failure is unrecoverable for a security boundary; use
		// a per-process value rather than a predictable constant.
		sum := sha256.Sum256([]byte(fmt.Sprintf("%p-%d", &key, time.Now().UnixNano())))
		return sum[:]
	}
	return key
}

func (a *app) markMCPConsentUsed(shape, full string) {
	if a.mcpConsents == nil {
		return
	}
	now := time.Now().UnixNano()
	for _, key := range []string{shape, full} {
		if approval, ok := a.mcpConsents[key]; ok {
			approval.LastUsed = now
			a.mcpConsents[key] = approval
		}
	}
	a.mcpConsents = pruneMCPConsents(a.mcpConsents)
	_ = saveMCPConsents(a.mcpConsentsPath, a.mcpConsents)
}

func (a *app) approveMCPConsent() {
	s := &a.mcpConsent
	if !s.open || s.d == nil || s.shape == "" || s.full == "" {
		return
	}
	if a.mcpConsents == nil {
		a.mcpConsents = map[string]mcpConsentApproval{}
	}
	now := time.Now().UnixNano()
	persist := s.d.assetID != ""
	a.mcpConsents[s.shape] = mcpConsentApproval{LastUsed: now, Persist: persist, Env: mcpConsentEnvHashes(s.plan, a.mcpConsentsKey)}
	a.mcpConsents[s.full] = mcpConsentApproval{LastUsed: now, Persist: persist}
	a.mcpConsents = pruneMCPConsents(a.mcpConsents)
	if err := saveMCPConsents(a.mcpConsentsPath, a.mcpConsents); err != nil {
		a.warn("MCP approval could not be saved; it will ask again next time.")
	}
	s.open = false
	d, plan := s.d, s.plan
	*s = mcpConsentState{}
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
			if s.valuesChanged {
				uikit.Hint(c, "environment values changed since you approved this command.")
				if len(s.changed) > 0 {
					uikit.Meta(c, "Changed values: "+strings.Join(s.changed, ", ")).SingleLine()
				}
			}
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
		*s = mcpConsentState{}
	}
}
