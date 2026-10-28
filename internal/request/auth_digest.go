package request

import (
	"cmp"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
)

// AuthDigest is the Auth type that answers an HTTP Digest challenge
// (RFC 7616, RFC 2617) with Username and Password. The first send carries
// no credentials; a 401 Digest challenge is answered by one more send.
const AuthDigest = "Digest"

// challenge is one WWW-Authenticate challenge: its scheme in lower case and
// its parameters by lower-case name.
type challenge struct {
	scheme string
	params map[string]string
}

// parseChallenges reads the challenges of WWW-Authenticate values, in order.
// A value may hold several challenges; quoted values may hold commas and
// escaped quotes. A token68 credential is skipped.
func parseChallenges(values []string) []challenge {
	var out []challenge
	space := func(c byte) bool { return c == ' ' || c == '\t' }
	for _, s := range values {
		i := 0
		for i < len(s) {
			for i < len(s) && (space(s[i]) || s[i] == ',') {
				i++
			}
			start := i
			for i < len(s) && !space(s[i]) && s[i] != ',' && s[i] != '=' {
				i++
			}
			if start == i { // A stray "=".
				i++
				continue
			}
			token := s[start:i]
			j := i
			for j < len(s) && space(s[j]) {
				j++
			}
			if j < len(s) && s[j] == '=' {
				k := j
				for k < len(s) && s[k] == '=' {
					k++
				}
				m := k
				for m < len(s) && space(s[m]) {
					m++
				}
				if k-j > 1 || m == len(s) || s[m] == ',' {
					i = k // token68 padding, or a parameter without a value
					continue
				}
				i = m
				var value string
				if s[i] == '"' {
					var b strings.Builder
					for i++; i < len(s) && s[i] != '"'; i++ {
						if s[i] == '\\' && i+1 < len(s) {
							i++
						}
						b.WriteByte(s[i])
					}
					i++
					value = b.String()
				} else {
					v := i
					for i < len(s) && !space(s[i]) && s[i] != ',' {
						i++
					}
					value = s[v:i]
				}
				if len(out) > 0 {
					out[len(out)-1].params[strings.ToLower(token)] = value
				}
				continue
			}
			out = append(out, challenge{scheme: strings.ToLower(token), params: map[string]string{}})
		}
	}
	return out
}

// digestHashes are the algorithms a Digest answer can use, by upper-case name
// without the -sess suffix.
var digestHashes = map[string]func() hash.Hash{"MD5": md5.New, "SHA-256": sha256.New}

// digestAuthorization answers the first Digest challenge of WWW-Authenticate
// values it supports: "" when there is no Digest challenge, an error when
// none can be answered.
func digestAuthorization(values []string, username, password, method, uri, cnonce string) (answer string, err error) {
	for _, c := range parseChallenges(values) {
		if c.scheme != "digest" {
			continue
		}
		algorithm := cmp.Or(c.params["algorithm"], "MD5")
		name, sess := strings.CutSuffix(strings.ToUpper(algorithm), "-SESS")
		newHash := digestHashes[name]
		if newHash == nil {
			err = cmp.Or(err, fmt.Errorf("the server asks for Digest algorithm %s; vscroxy supports MD5, MD5-sess, SHA-256 and SHA-256-sess", algorithm))
			continue
		}
		qop := ""
		if offered, ok := c.params["qop"]; ok {
			var options []string
			for option := range strings.SplitSeq(offered, ",") {
				options = append(options, strings.ToLower(strings.TrimSpace(option)))
			}
			if !slices.Contains(options, "auth") {
				err = cmp.Or(err, fmt.Errorf("the server asks for Digest qop %q; vscroxy supports only qop=auth", offered))
				continue
			}
			qop = "auth"
		}
		h := func(parts ...string) string {
			sum := newHash()
			sum.Write([]byte(strings.Join(parts, ":")))
			return hex.EncodeToString(sum.Sum(nil))
		}
		realm, nonce := c.params["realm"], c.params["nonce"]
		ha1 := h(username, realm, password)
		if sess {
			ha1 = h(ha1, nonce, cnonce)
		}
		ha2 := h(method, uri)
		const nc = "00000001"
		response := h(ha1, nonce, ha2)
		if qop != "" {
			response = h(ha1, nonce, nc, cnonce, qop, ha2)
		}
		quote := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace
		answer = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s"`, quote(username), quote(realm), quote(nonce), quote(uri))
		if _, ok := c.params["algorithm"]; ok {
			answer += ", algorithm=" + algorithm
		}
		if qop != "" {
			answer += ", qop=" + qop + ", nc=" + nc
		}
		if qop != "" || sess {
			answer += `, cnonce="` + quote(cnonce) + `"`
		}
		answer += `, response="` + response + `"`
		if opaque, ok := c.params["opaque"]; ok {
			answer += `, opaque="` + quote(opaque) + `"`
		}
		return answer, nil
	}
	return "", err
}

// digestAnswer is the Authorization header that answers res when it is a 401
// Digest challenge to req, "" when it is not one.
func digestAnswer(res capture.Flow, req *Prepared, username, password string) (string, error) {
	if res.Status != http.StatusUnauthorized {
		return "", nil
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return "", err
	}
	cnonce := make([]byte, 16)
	rand.Read(cnonce)
	return digestAuthorization(res.ResponseHeader.Values("WWW-Authenticate"), username, password, req.Method, u.RequestURI(), hex.EncodeToString(cnonce))
}
