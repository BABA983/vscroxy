package app

import (
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/store"
)

// maxProtoTypeRules bounds the saved message-type rules.
const maxProtoTypeRules = 200

// protoTypeRule says which protobuf message a URL carries, so the inspector
// opens the right type without choosing it again.
type protoTypeRule struct {
	Match string `json:"match"`
	Type  string `json:"type"`
}

type protoTypeFile struct {
	Version int             `json:"version,omitempty"`
	Rules   []protoTypeRule `json:"rules"`
}

var protoTypeName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

func validateProtoTypeRules(rules []protoTypeRule) error {
	if len(rules) > maxProtoTypeRules {
		return fmt.Errorf("keep at most %d protobuf message rules", maxProtoTypeRules)
	}
	for i, r := range rules {
		if r.Match == "" || r.Match != strings.TrimSpace(r.Match) || len(r.Match) > 2048 {
			return fmt.Errorf("rule %d: match must be trimmed text of 1–2048 bytes", i+1)
		}
		if !protoTypeName.MatchString(r.Type) {
			return fmt.Errorf("rule %d: %q is not a protobuf message type name", i+1, r.Type)
		}
	}
	return nil
}

// loadProtoTypeRules reads the saved rules; a damaged file leaves the last
// good rules in place and is reported.
func (a *app) loadProtoTypeRules() {
	if a.protoTypesPath == "" {
		return
	}
	f := &store.File{Path: a.protoTypesPath, Format: store.ProtobufTypes, BackupDir: store.BackupDirFor(a.protoTypesPath)}
	data, err := f.Read()
	if err != nil {
		a.protoTypesProblem = err.Error()
		return
	}
	if data == nil {
		return
	}
	var file protoTypeFile
	if err := json.Unmarshal(data, &file, json.RejectUnknownMembers(true)); err != nil {
		a.protoTypesProblem = "Saved protobuf message rules: " + err.Error()
		return
	}
	if err := validateProtoTypeRules(file.Rules); err != nil {
		a.protoTypesProblem = "Saved protobuf message rules: " + err.Error()
		return
	}
	a.protoTypes, a.protoTypesProblem = file.Rules, ""
}

// saveProtoTypeRules validates the complete list before writing it.
func (a *app) saveProtoTypeRules(rules []protoTypeRule) bool {
	if err := validateProtoTypeRules(rules); err != nil {
		a.protoTypesProblem = err.Error()
		return false
	}
	if a.protoTypesPath == "" {
		a.protoTypesProblem = "Protobuf message rules location is unavailable"
		return false
	}
	data, err := json.Marshal(protoTypeFile{Version: store.Version, Rules: rules})
	if err == nil {
		f := &store.File{Path: a.protoTypesPath, Format: store.ProtobufTypes, BackupDir: store.BackupDirFor(a.protoTypesPath)}
		if _, err = f.Read(); err == nil {
			err = f.Write(append(data, '\n'))
		}
	}
	if err != nil {
		a.protoTypesProblem = err.Error()
		return false
	}
	a.protoTypes, a.protoTypesProblem = slices.Clone(rules), ""
	return true
}

// protoTypeRuleFor is the message type saved for url: the longest matching
// literal match wins.
func (a *app) protoTypeRuleFor(url string) string {
	best, name := -1, ""
	for _, r := range a.protoTypes {
		if strings.Contains(url, r.Match) && len(r.Match) > best {
			best, name = len(r.Match), r.Type
		}
	}
	return name
}

// saveProtoTypeRule remembers typeName for url, replacing an earlier rule for
// the same URL.
func (a *app) saveProtoTypeRule(url, typeName string) bool {
	rules := slices.Clone(a.protoTypes)
	if i := slices.IndexFunc(rules, func(r protoTypeRule) bool { return r.Match == url }); i >= 0 {
		rules[i].Type = typeName
	} else {
		rules = append(rules, protoTypeRule{Match: url, Type: typeName})
	}
	return a.saveProtoTypeRules(rules)
}

// forgetProtoTypeRule removes the rule saved for exactly url.
func (a *app) forgetProtoTypeRule(url string) bool {
	rules := slices.DeleteFunc(slices.Clone(a.protoTypes), func(r protoTypeRule) bool { return r.Match == url })
	return a.saveProtoTypeRules(rules)
}

// applyURLRule chooses the message type saved for url, or Auto when none is.
func (p *protoInspector) applyURLRule(a *app, url string) {
	if p.ruleFor == url {
		return
	}
	p.ruleFor = url
	if name := a.protoTypeRuleFor(url); name != "" {
		p.name = name
	} else if p.name != "Auto" {
		p.name = "Auto"
	}
}
