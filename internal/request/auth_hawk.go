package request

import (
	"cmp"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
)

// AuthHawk is the Auth type that signs a request with Hawk.
const AuthHawk = "Hawk"

// HawkAlgorithms lists the MAC hashes Hawk offers, the default first.
var HawkAlgorithms = []string{"SHA256", "SHA1"}

// HawkAuth is how a request is signed with Hawk. Every text field may use
// {{variables}}.
type HawkAuth struct {
	AuthID string `json:"authId,omitempty"`
	AuthKey string `json:"authKey,omitempty"`
	// Algorithm is SHA256 (empty) or SHA1.
	Algorithm string `json:"algorithm,omitempty"`
	// User is Postman's Hawk user; the request MAC does not cover it.
	User string `json:"user,omitempty"`
	// Nonce and Timestamp are fixed when set; empty generates them per send.
	Nonce     string `json:"nonce,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// ExtraData is the ext attribute.
	ExtraData string `json:"extraData,omitempty"`
	// App and Delegation are the Oz application and delegated-by IDs.
	App        string `json:"app,omitempty"`
	Delegation string `json:"delegation,omitempty"`
	// IncludePayloadHash adds hash, the payload's content hash, to the MAC.
	IncludePayloadHash bool `json:"includePayloadHash,omitzero"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (h *HawkAuth) fields() []*string {
	return []*string{&h.AuthID, &h.AuthKey, &h.User, &h.Nonce, &h.Timestamp, &h.ExtraData, &h.App, &h.Delegation}
}

func (h HawkAuth) expand(vars map[string]string) HawkAuth {
	for _, f := range h.fields() {
		*f = ExpandVars(*f, vars)
	}
	return h
}

// sign adds the Hawk Authorization header to req.
func (h HawkAuth) sign(req *Prepared) error {
	if h.AuthID == "" || h.AuthKey == "" {
		return errors.New("Hawk needs an auth ID and auth key")
	}
	algorithm := strings.ToLower(strings.TrimSpace(h.Algorithm))
	if algorithm == "" {
		algorithm = "sha256"
	}
	var newHash func() hash.Hash
	switch algorithm {
	case "sha1":
		newHash = sha1.New
	case "sha256":
		newHash = sha256.New
	default:
		return fmt.Errorf("Hawk algorithm %q is not supported; use SHA256 or SHA1", h.Algorithm)
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return errors.New("Hawk needs an absolute URL with a host")
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return errors.New("Hawk needs an HTTP(S) URL")
		}
	}
	timestamp := cmp.Or(h.Timestamp, strconv.FormatInt(now().Unix(), 10))
	nonce := cmp.Or(h.Nonce, hawkNonce())

	payloadHash := ""
	if h.IncludePayloadHash {
		sum := newHash()
		sum.Write([]byte("hawk.1.payload\n" + hawkContentType(req.Header.Get("Content-Type")) + "\n" + req.Body + "\n"))
		payloadHash = base64.StdEncoding.EncodeToString(sum.Sum(nil))
	}

	resource := u.EscapedPath()
	if resource == "" {
		resource = "/"
	}
	if u.RawQuery != "" {
		resource += "?" + u.RawQuery
	}
	normalized := "hawk.1.header\n" +
		timestamp + "\n" +
		nonce + "\n" +
		cmp.Or(strings.ToUpper(strings.TrimSpace(req.Method)), "GET") + "\n" +
		resource + "\n" +
		strings.ToLower(u.Hostname()) + "\n" +
		port + "\n" +
		payloadHash + "\n" +
		hawkExt(h.ExtraData) + "\n"
	if h.App != "" {
		normalized += h.App + "\n" + h.Delegation + "\n"
	}

	mac := hmac.New(newHash, []byte(h.AuthKey))
	mac.Write([]byte(normalized))
	header := []string{
		`id="` + hawkHeaderValue(h.AuthID) + `"`,
		`ts="` + hawkHeaderValue(timestamp) + `"`,
		`nonce="` + hawkHeaderValue(nonce) + `"`,
	}
	if payloadHash != "" {
		header = append(header, `hash="`+payloadHash+`"`)
	}
	if h.ExtraData != "" {
		header = append(header, `ext="`+hawkHeaderValue(h.ExtraData)+`"`)
	}
	header = append(header, `mac="`+base64.StdEncoding.EncodeToString(mac.Sum(nil))+`"`)
	if h.App != "" {
		header = append(header, `app="`+hawkHeaderValue(h.App)+`"`)
		if h.Delegation != "" {
			header = append(header, `dlg="`+hawkHeaderValue(h.Delegation)+`"`)
		}
	}
	req.Set("Authorization", "Hawk "+strings.Join(header, ", "))
	return nil
}

// hawkContentType is the lower-case media type without its parameters.
func hawkContentType(value string) string {
	value, _, _ = strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(value))
}

// hawkExt is ext as the normalized string carries it: backslashes and line
// breaks escaped, so the MAC covers one line.
func hawkExt(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(value)
}

// hawkHeaderValue escapes a value for a quoted header attribute.
func hawkHeaderValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(value)
}

// hawkNonce is six random letters or digits.
func hawkNonce() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(now().UnixNano(), 36)[:6]
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
