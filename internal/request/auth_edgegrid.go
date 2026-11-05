package request

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// AuthEdgeGrid is the Auth type that signs a request with Akamai EdgeGrid.
const AuthEdgeGrid = "Akamai EdgeGrid"

// EdgeGridAuth is how a request is signed for Akamai EdgeGrid. Every text
// field may use {{variables}}.
type EdgeGridAuth struct {
	AccessToken  string `json:"accessToken,omitempty"`
	ClientToken  string `json:"clientToken,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	// BaseURL overrides the host the signature covers; empty uses the
	// request URL's host. A scheme and path are ignored.
	BaseURL string `json:"baseURL,omitempty"`
	// Nonce and Timestamp are fixed when set; empty generates them per send.
	Nonce     string `json:"nonce,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// HeadersToSign is a comma-separated list of header names the signature
	// covers, in order.
	HeadersToSign string `json:"headersToSign,omitempty"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (e *EdgeGridAuth) fields() []*string {
	return []*string{&e.AccessToken, &e.ClientToken, &e.ClientSecret, &e.BaseURL, &e.Nonce, &e.Timestamp, &e.HeadersToSign}
}

func (e EdgeGridAuth) expand(vars map[string]string) EdgeGridAuth {
	for _, f := range e.fields() {
		*f = ExpandVars(*f, vars)
	}
	return e
}

// sign adds the EdgeGrid Authorization header to req.
func (e EdgeGridAuth) sign(req *Prepared) error {
	if e.AccessToken == "" || e.ClientToken == "" || e.ClientSecret == "" {
		return errors.New("Akamai EdgeGrid needs an access token, client token and client secret")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return errors.New("Akamai EdgeGrid needs an absolute URL with a host")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("Akamai EdgeGrid needs an HTTP(S) URL")
	}
	host := u.Host
	if base := strings.TrimSpace(e.BaseURL); base != "" {
		if !strings.Contains(base, "://") {
			base = "https://" + base
		}
		parsed, err := url.Parse(base)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("Akamai EdgeGrid base URL %q is not a URL with a host", e.BaseURL)
		}
		host = parsed.Host
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	timestamp := cmp.Or(strings.TrimSpace(e.Timestamp), now().UTC().Format("20060102T15:04:05-0700"))
	nonce := cmp.Or(strings.TrimSpace(e.Nonce), uuid.NewString())
	auth := "EG1-HMAC-SHA256 client_token=" + e.ClientToken + ";access_token=" + e.AccessToken +
		";timestamp=" + timestamp + ";nonce=" + nonce + ";"

	contentHash := ""
	if cmp.Or(strings.ToUpper(strings.TrimSpace(req.Method)), "GET") == "POST" && req.Body != "" {
		sum := sha256.Sum256([]byte(req.Body))
		contentHash = base64.StdEncoding.EncodeToString(sum[:])
	}
	data := strings.Join([]string{
		cmp.Or(strings.ToUpper(strings.TrimSpace(req.Method)), "GET"),
		scheme,
		host,
		path,
		edgeGridHeaders(e.HeadersToSign, req.Header, u.Host),
		contentHash,
		auth,
	}, "\t")
	signingKey := base64.StdEncoding.EncodeToString(hmacSHA256([]byte(e.ClientSecret), timestamp))
	signature := base64.StdEncoding.EncodeToString(hmacSHA256([]byte(signingKey), data))
	req.Set("Authorization", auth+"signature="+signature)
	return nil
}

// edgeGridHeaders canonicalizes the configured headers: lower-case names in
// their configured order, values trimmed with runs of whitespace collapsed.
func edgeGridHeaders(list string, header http.Header, requestHost string) string {
	var lines []string
	seen := map[string]bool{}
	for name := range strings.SplitSeq(list, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		values := header.Values(name)
		if name == "host" && len(values) == 0 {
			values = []string{requestHost}
		}
		if len(values) == 0 {
			continue
		}
		value := strings.Join(values, ",")
		lines = append(lines, name+":"+strings.Join(strings.Fields(value), " "))
	}
	return strings.Join(lines, "\t")
}
