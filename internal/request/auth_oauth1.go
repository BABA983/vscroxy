package request

import (
	"cmp"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	_ "crypto/sha512" // Registers crypto.SHA512 for RSA-SHA512.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AuthOAuth1 is the Auth type that signs a request with OAuth 1.0
// (RFC 5849).
const AuthOAuth1 = "OAuth 1.0"

// OAuth1Methods lists the signature methods OAuth 1.0 offers, the default
// first.
var OAuth1Methods = []string{"HMAC-SHA1", "HMAC-SHA256", "PLAINTEXT", "RSA-SHA1", "RSA-SHA256", "RSA-SHA512"}

// OAuth1Auth is how a request is signed for OAuth 1.0. Every text field may
// use {{variables}}.
type OAuth1Auth struct {
	ConsumerKey    string `json:"consumerKey,omitempty"`
	ConsumerSecret string `json:"consumerSecret,omitempty"`
	Token          string `json:"token,omitempty"`
	TokenSecret    string `json:"tokenSecret,omitempty"`
	// PrivateKey is the PEM key RSA-SHA1/256/512 signs with.
	PrivateKey string `json:"privateKey,omitempty"`
	// SignatureMethod is one of OAuth1Methods, empty for HMAC-SHA1.
	SignatureMethod string `json:"signatureMethod,omitempty"`
	// Timestamp and Nonce are fixed when set; empty generates them per send.
	Timestamp string `json:"timestamp,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
	// Version is oauth_version; empty omits it, and a configured auth
	// defaults it to 1.0.
	Version string `json:"version,omitempty"`
	Realm   string `json:"realm,omitempty"`
	// AddTo is "Header", "Query" or "Body"; "Auto" follows Postman and uses
	// the form body for a POST/PUT, the query otherwise. Empty is Header.
	AddTo string `json:"addTo,omitempty"`
	// IncludeBodyHash adds oauth_body_hash for a body that is not
	// application/x-www-form-urlencoded.
	IncludeBodyHash      bool   `json:"includeBodyHash,omitzero"`
	AddEmptyParamsToSign bool   `json:"addEmptyParamsToSign,omitzero"`
	Callback             string `json:"callback,omitempty"`
	Verifier             string `json:"verifier,omitempty"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (a *OAuth1Auth) fields() []*string {
	return []*string{&a.ConsumerKey, &a.ConsumerSecret, &a.Token, &a.TokenSecret, &a.PrivateKey,
		&a.Timestamp, &a.Nonce, &a.Version, &a.Realm, &a.Callback, &a.Verifier}
}

func (a OAuth1Auth) expand(vars map[string]string) OAuth1Auth {
	for _, f := range a.fields() {
		*f = ExpandVars(*f, vars)
	}
	return a
}

// defaults fills the method and protocol version a configured auth sends.
// The RFC vector tests call sign without it to omit oauth_version.
func (a OAuth1Auth) defaults() OAuth1Auth {
	a.SignatureMethod = cmp.Or(strings.ToUpper(strings.TrimSpace(a.SignatureMethod)), "HMAC-SHA1")
	a.Version = cmp.Or(strings.TrimSpace(a.Version), "1.0")
	return a
}

type oauth1Pair struct {
	name, value string
}

// sign adds OAuth 1.0 parameters and their signature to req. It removes
// parameters an earlier signing added, so signing again replaces them.
func (a OAuth1Auth) sign(req *Prepared, t time.Time) error {
	method := cmp.Or(strings.ToUpper(strings.TrimSpace(req.Method)), "GET")
	signatureMethod := cmp.Or(strings.ToUpper(strings.TrimSpace(a.SignatureMethod)), "HMAC-SHA1")
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return errors.New("OAuth 1.0 needs an absolute URL with a host")
	}
	if a.ConsumerKey == "" {
		return errors.New("OAuth 1.0 needs a consumer key")
	}
	switch {
	case signatureMethod == "PLAINTEXT" || strings.HasPrefix(signatureMethod, "HMAC-"):
		if a.ConsumerSecret == "" {
			return errors.New("OAuth 1.0 needs a consumer secret for " + signatureMethod)
		}
	case strings.HasPrefix(signatureMethod, "RSA-"):
		if strings.TrimSpace(a.PrivateKey) == "" {
			return errors.New("OAuth 1.0 needs a PEM private key for " + signatureMethod)
		}
	default:
		return fmt.Errorf("OAuth 1.0 signature method %q is not supported; use one of %s", a.SignatureMethod, strings.Join(OAuth1Methods, ", "))
	}

	contentType := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))
	formBody := strings.HasPrefix(contentType, "application/x-www-form-urlencoded")
	addTo := a.AddTo
	switch addTo {
	case "", "Header":
		addTo = "Header"
	case "Query", "Body", "Auto":
	default:
		return fmt.Errorf("OAuth 1.0 cannot add its parameters to %q", a.AddTo)
	}
	if addTo == "Auto" {
		if (method == "POST" || method == "PUT") && formBody {
			addTo = "Body"
		} else {
			addTo = "Query"
		}
	}
	if addTo == "Body" && !formBody && strings.TrimSpace(req.Body) != "" {
		return errors.New("OAuth 1.0 can put its parameters in the body only for an application/x-www-form-urlencoded body")
	}

	// Parameters from an earlier signing are replaced, not signed again.
	u.RawQuery = oauth1WithoutParams(u.RawQuery)
	if formBody {
		req.Body = oauth1WithoutParams(req.Body)
	}
	// Structured auth owns Authorization, wherever its parameters go.
	req.Header.Del("Authorization")

	params := []oauth1Pair{{"oauth_consumer_key", a.ConsumerKey}}
	if a.Token != "" || a.AddEmptyParamsToSign {
		params = append(params, oauth1Pair{"oauth_token", a.Token})
	}
	params = append(params,
		oauth1Pair{"oauth_signature_method", signatureMethod},
		oauth1Pair{"oauth_timestamp", cmp.Or(a.Timestamp, strconv.FormatInt(t.Unix(), 10))},
		oauth1Pair{"oauth_nonce", cmp.Or(a.Nonce, oauth1Nonce())},
	)
	if a.Version != "" || a.AddEmptyParamsToSign {
		params = append(params, oauth1Pair{"oauth_version", a.Version})
	}
	if a.Callback != "" {
		params = append(params, oauth1Pair{"oauth_callback", a.Callback})
	}
	if a.Verifier != "" {
		params = append(params, oauth1Pair{"oauth_verifier", a.Verifier})
	}

	var hashID crypto.Hash
	switch signatureMethod {
	case "HMAC-SHA1", "RSA-SHA1":
		hashID = crypto.SHA1
	case "HMAC-SHA256", "RSA-SHA256":
		hashID = crypto.SHA256
	case "RSA-SHA512":
		hashID = crypto.SHA512
	}
	if a.IncludeBodyHash && !formBody && hashID != 0 {
		sum := hashID.New()
		sum.Write([]byte(req.Body))
		params = append(params, oauth1Pair{"oauth_body_hash", base64.StdEncoding.EncodeToString(sum.Sum(nil))})
	}

	// The base string covers the query and form parameters, the OAuth
	// parameters added above, and never oauth_signature itself.
	signatureParams := slices.Clone(params)
	query, err := oauth1QueryPairs(u.RawQuery)
	if err != nil {
		return err
	}
	signatureParams = append(signatureParams, query...)
	if formBody {
		form, err := oauth1QueryPairs(req.Body)
		if err != nil {
			return err
		}
		signatureParams = append(signatureParams, form...)
	}
	base := method + "&" + percentEscape(oauth1BaseURL(u)) + "&" + percentEscape(oauth1Normalize(signatureParams))
	signature, err := oauth1Signature(a, signatureMethod, hashID, base)
	if err != nil {
		return err
	}
	params = append(params, oauth1Pair{"oauth_signature", signature})

	switch addTo {
	case "Header":
		req.Header.Set("Authorization", oauth1Header(a.Realm, params))
	case "Query":
		u.RawQuery = oauth1Append(u.RawQuery, params)
		req.URL = u.String()
	case "Body":
		req.Body = oauth1Append(req.Body, params)
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	return nil
}

// oauth1Signature is the signature of base with the configured method.
func oauth1Signature(a OAuth1Auth, method string, hashID crypto.Hash, base string) (string, error) {
	switch method {
	case "HMAC-SHA1", "HMAC-SHA256":
		var newHash func() hash.Hash
		if hashID == crypto.SHA1 {
			newHash = sha1.New
		} else {
			newHash = sha256.New
		}
		key := percentEscape(a.ConsumerSecret) + "&" + percentEscape(a.TokenSecret)
		mac := hmac.New(newHash, []byte(key))
		mac.Write([]byte(base))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
	case "PLAINTEXT":
		return percentEscape(a.ConsumerSecret) + "&" + percentEscape(a.TokenSecret), nil
	}
	key, err := jwtPrivateKey(a.PrivateKey)
	if err != nil {
		return "", err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("%s needs an RSA private key", method)
	}
	sum := hashID.New()
	sum.Write([]byte(base))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, hashID, sum.Sum(nil))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// oauth1BaseURL is the scheme, authority and path of u, without the query:
// lower case, the default port removed, and the path as sent.
func oauth1BaseURL(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !(scheme == "http" && port == "80") && !(scheme == "https" && port == "443") {
		host += ":" + port
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

// oauth1Normalize sorts pairs by encoded name and value and joins them.
func oauth1Normalize(pairs []oauth1Pair) string {
	encoded := make([]oauth1Pair, len(pairs))
	for i, p := range pairs {
		encoded[i] = oauth1Pair{percentEscape(p.name), percentEscape(p.value)}
	}
	slices.SortFunc(encoded, func(a, b oauth1Pair) int {
		return cmp.Or(strings.Compare(a.name, b.name), strings.Compare(a.value, b.value))
	})
	parts := make([]string, len(encoded))
	for i, p := range encoded {
		parts[i] = p.name + "=" + p.value
	}
	return strings.Join(parts, "&")
}

// oauth1QueryPairs reads an application/x-www-form-urlencoded string into
// decoded pairs, keeping repeats.
func oauth1QueryPairs(raw string) ([]oauth1Pair, error) {
	var pairs []oauth1Pair
	for part := range strings.SplitSeq(raw, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name, err := url.QueryUnescape(name)
		if err != nil {
			return nil, fmt.Errorf("OAuth 1.0 cannot read a parameter name: %w", err)
		}
		value, err = url.QueryUnescape(value)
		if err != nil {
			return nil, fmt.Errorf("OAuth 1.0 cannot read the value of %s: %w", name, err)
		}
		pairs = append(pairs, oauth1Pair{name, value})
	}
	return pairs, nil
}

// oauth1WithoutParams removes oauth_* pairs from an encoded query or form
// body, keeping every other pair as it was encoded.
func oauth1WithoutParams(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "&")
	kept := parts[:0]
	for _, part := range parts {
		name := part
		if i := strings.IndexByte(part, '='); i >= 0 {
			name = part[:i]
		}
		decoded, err := url.QueryUnescape(name)
		if err == nil && strings.HasPrefix(decoded, "oauth_") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

// oauth1Append adds percent-encoded pairs to an encoded query or form body.
func oauth1Append(raw string, pairs []oauth1Pair) string {
	var b strings.Builder
	b.WriteString(raw)
	for _, p := range pairs {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "&") {
			b.WriteByte('&')
		}
		b.WriteString(percentEscape(p.name))
		b.WriteByte('=')
		b.WriteString(percentEscape(p.value))
	}
	return b.String()
}

// oauth1Header is the Authorization value: realm first, then every
// parameter, both names and values percent-encoded.
func oauth1Header(realm string, params []oauth1Pair) string {
	var b strings.Builder
	b.WriteString("OAuth ")
	if realm != "" {
		b.WriteString(`realm="` + percentEscape(realm) + `"`)
		if len(params) > 0 {
			b.WriteString(", ")
		}
	}
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(percentEscape(p.name) + `="` + percentEscape(p.value) + `"`)
	}
	return b.String()
}

// oauth1Nonce is 16 random bytes as hex.
func oauth1Nonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)
}

// oauth1AddsToHeader reports whether a's parameters go in Authorization
// before the request body is known; Auto never does.
func oauth1AddsToHeader(a OAuth1Auth) bool {
	return a.AddTo == "" || a.AddTo == "Header"
}
