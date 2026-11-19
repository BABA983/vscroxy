package app

import (
	"reflect"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// unicodeAuthString is one authored credential or auth setting, named as the
// editor names it so a send warning can open that field.
type unicodeAuthString struct {
	label string
	key   string
	value string
}

// unicodeAuthStrings walks every auth type's string fields. The map of fields
// is small, and this keeps the send check in step with the Auth model as new
// schemes are added, without repeating each scheme's field list here.
func unicodeAuthStrings(auth request.Auth) []unicodeAuthString {
	var out []unicodeAuthString
	collectAuthStrings(reflect.ValueOf(auth), "Auth", &out)
	return out
}

func collectAuthStrings(value reflect.Value, path string, out *[]unicodeAuthString) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return
	}
	typeOf := value.Type()
	for i := range value.NumField() {
		field := typeOf.Field(i)
		if !field.IsExported() {
			continue
		}
		if path == "Auth" && (field.Name == "Type" || field.Name == "Location") {
			continue // the auth scheme and where it goes are not authored values
		}
		name := path + "." + field.Name
		member := value.Field(i)
		for member.Kind() == reflect.Pointer {
			if member.IsNil() {
				break
			}
			member = member.Elem()
		}
		switch member.Kind() {
		case reflect.String:
			if member.String() == "" {
				continue
			}
			label, key := unicodeAuthFieldName(name)
			*out = append(*out, unicodeAuthString{label: label, key: key, value: member.String()})
		case reflect.Struct:
			if member.Type().PkgPath() == "time" {
				continue
			}
			collectAuthStrings(member, name, out)
		}
	}
}

// unicodeAuthFieldName turns a field path into the name the field editor uses.
// OAuth 2 has its own input keys; the rest are under "auth-<label>".
func unicodeAuthFieldName(path string) (label, key string) {
	switch path {
	case "Auth.Token":
		label = "Bearer token"
	case "Auth.Key":
		label = "API key name"
	case "Auth.Value":
		label = "API key value"
	}
	if label == "" {
		label = strings.TrimPrefix(strings.ReplaceAll(path, "Auth.", ""), "OAuth2.")
	}
	if strings.HasPrefix(path, "Auth.OAuth2.") {
		return "OAuth 2 · " + label, "oauth-" + label
	}
	return label, "auth-" + label
}
