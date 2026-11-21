// Package ntlm builds NTLMv2 messages and reads NTLM challenges. It is the
// one NTLM implementation for authored requests and upstream proxies; it does
// not import MyGo. Only NTLMv2 is supported.
package ntlm

import (
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/Azure/go-ntlmssp"
)

// The schemes an HTTP proxy uses for NTLM. A proxy may offer NTLM directly or
// carry the same messages inside a Negotiate challenge.
const (
	SchemeNTLM      = "NTLM"
	SchemeNegotiate = "Negotiate"
)

// Credentials is one NTLMv2 account. Domain and Workstation are optional; an
// empty domain comes from the username's DOMAIN\user or user@domain form.
type Credentials struct {
	Username    string
	Password    string
	Domain      string
	Workstation string
}

// UserDomain splits a username into the account and its domain. A non-empty
// domain wins and the username is sent as written; otherwise DOMAIN\user or
// user@domain supply the domain.
func UserDomain(username, domain string) (user, userDomain string) {
	username, domain = strings.TrimSpace(username), strings.TrimSpace(domain)
	if domain != "" {
		return username, domain
	}
	if strings.Contains(username, `\`) && strings.Contains(username, "@") {
		return username, ""
	}
	if before, after, ok := strings.Cut(username, `\`); ok && before != "" && after != "" && !strings.Contains(after, `\`) {
		return after, before
	}
	if before, after, ok := strings.Cut(username, "@"); ok && before != "" && after != "" && !strings.Contains(after, "@") {
		return before, after
	}
	return username, ""
}

// NegotiateMessage builds the Type 1 message that starts the exchange.
func (c Credentials) NegotiateMessage() ([]byte, error) {
	_, domain := UserDomain(c.Username, c.Domain)
	return ntlmssp.NewNegotiateMessage(domain, c.Workstation)
}

// AuthenticateMessage answers a Type 2 challenge with the Type 3 message.
func (c Credentials) AuthenticateMessage(challenge []byte) ([]byte, error) {
	user, domain := UserDomain(c.Username, c.Domain)
	account := user
	if domain != "" {
		account = domain + `\` + user
	}
	return ntlmssp.NewAuthenticateMessage(challenge, account, c.Password,
		&ntlmssp.AuthenticateMessageOptions{WorkstationName: c.Workstation})
}

// Challenge reports whether WWW-Authenticate values offer NTLM and returns the
// first Type 2 message they carry. A bare "NTLM" challenge has none yet.
func Challenge(values []string) (type2 []byte, offered bool) {
	for _, c := range parse(values) {
		if !strings.EqualFold(c.scheme, SchemeNTLM) {
			continue
		}
		offered = true
		if len(c.token) > 0 {
			return c.token, true
		}
	}
	return nil, offered
}

// ProxyChallenge picks how to answer a proxy's Proxy-Authenticate values. It
// prefers a Type 2 token in either scheme, then a bare NTLM challenge, then a
// bare Negotiate challenge. A Negotiate token that is not NTLM (such as a
// Kerberos token) is treated as a bare challenge so the caller can offer NTLM
// inside Negotiate.
func ProxyChallenge(values []string) (scheme string, type2 []byte, offered bool) {
	challenges := parse(values)
	for _, c := range challenges {
		if c.isNTLMType2() {
			return c.scheme, c.token, true
		}
	}
	for _, c := range challenges {
		if strings.EqualFold(c.scheme, SchemeNTLM) {
			return SchemeNTLM, nil, true
		}
	}
	for _, c := range challenges {
		if strings.EqualFold(c.scheme, SchemeNegotiate) {
			return SchemeNegotiate, nil, true
		}
	}
	return "", nil, false
}

// challenge is one scheme and its optional decoded token.
type challenge struct {
	scheme string
	token  []byte
}

// isNTLMType2 reports whether the challenge carries an NTLM Type 2 message.
// A Negotiate token may instead be a Kerberos token, which this client cannot
// answer.
func (c challenge) isNTLMType2() bool {
	if len(c.token) < 12 || string(c.token[:8]) != "NTLMSSP\x00" {
		return false
	}
	return binary.LittleEndian.Uint32(c.token[8:12]) == 2
}

// parse reads the challenges in WWW-Authenticate or Proxy-Authenticate
// values. A scheme may be followed by a base64 token; parameters such as
// Basic's realm are skipped.
func parse(values []string) []challenge {
	var out []challenge
	for _, value := range values {
		for i := 0; i < len(value); {
			switch value[i] {
			case ' ', '\t', ',':
				i++
				continue
			case '"':
				i++
				for i < len(value) && value[i] != '"' {
					if value[i] == '\\' {
						i++
					}
					i++
				}
				i++
				continue
			}
			start := i
			for i < len(value) && !strings.ContainsRune(" \t,=\"", rune(value[i])) {
				i++
			}
			if start == i {
				i++
				continue
			}
			scheme := value[start:i]
			j := i
			for j < len(value) && (value[j] == ' ' || value[j] == '\t') {
				j++
			}
			if j < len(value) && value[j] == '=' {
				// A parameter name and value: skip to the next challenge.
				i = j + 1
				for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
					i++
				}
				if i < len(value) && value[i] == '"' {
					i++
					for i < len(value) && value[i] != '"' {
						if value[i] == '\\' {
							i++
						}
						i++
					}
					i++
					continue
				}
				for i < len(value) && value[i] != ',' {
					i++
				}
				continue
			}
			if !strings.EqualFold(scheme, SchemeNTLM) && !strings.EqualFold(scheme, SchemeNegotiate) {
				// Another scheme's parameters follow; parse them on the next
				// iteration instead of taking them for a token.
				i = j
				continue
			}
			c := challenge{scheme: scheme}
			tokenStart := j
			for j < len(value) && !strings.ContainsRune(" \t,", rune(value[j])) {
				j++
			}
			if raw := value[tokenStart:j]; raw != "" {
				if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
					c.token = decoded
				}
			}
			out = append(out, c)
			i = j
		}
	}
	return out
}
