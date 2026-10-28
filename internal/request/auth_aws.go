package request

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

// AuthAWS is the Auth type that signs a request with AWS Signature Version 4.
const AuthAWS = "AWS Signature"

// AWSAuth is how a request is signed for AWS. Every field may use
// {{variables}}. The signature goes in headers, never in the query.
type AWSAuth struct {
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	// Region is empty for us-east-1.
	Region string `json:"region,omitempty"`
	// Service is the signing name, such as s3, execute-api or iam.
	Service      string `json:"service,omitempty"`
	SessionToken string `json:"sessionToken,omitempty"`
}

// now is the clock requests are signed with; tests fix it.
var now = time.Now

// fields are the configuration's texts, for expansion and variable lists.
func (a *AWSAuth) fields() []*string {
	return []*string{&a.AccessKey, &a.SecretKey, &a.Region, &a.Service, &a.SessionToken}
}

func (a AWSAuth) expand(vars map[string]string) AWSAuth {
	for _, f := range a.fields() {
		*f = strings.TrimSpace(ExpandVars(*f, vars))
	}
	return a
}

// awsHeaders are the headers a signature sets. Signing drops them first, so
// signing a request again replaces its signature.
var awsHeaders = []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Security-Token"}

// sign signs req at t. It must be the last change to req: the signature
// covers the method, URL, body, Content-Type and every X-Amz-* header.
func (a AWSAuth) sign(req *Prepared, t time.Time) error {
	if a.Service == "" {
		return errors.New("AWS Signature needs a service name, such as s3, execute-api or iam")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("AWS Signature needs an absolute URL with a host")
	}
	if req.Header == nil {
		req.Header = http.Header{}
	}
	for _, name := range awsHeaders {
		req.Header.Del(name)
	}
	sum := sha256.Sum256([]byte(req.Body))
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Date", t.UTC().Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payload)
	if a.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", a.SessionToken)
	}
	signed := http.Header{}
	for name, values := range req.Header {
		if lower := strings.ToLower(name); lower == "content-type" || strings.HasPrefix(lower, "x-amz-") {
			signed[name] = values
		}
	}
	req.Header.Set("Authorization", a.authorization(req.Method, u.Host, u.EscapedPath(), u.RawQuery, signed, payload, t))
	return nil
}

// authorization is the Authorization value for a request to host with the
// path and query as sent, signing host and the headers in signed.
func (a AWSAuth) authorization(method, host, rawPath, rawQuery string, signed http.Header, payload string, t time.Time) string {
	t = t.UTC()
	date := t.Format("20060102")
	scope := date + "/" + cmp.Or(a.Region, "us-east-1") + "/" + a.Service + "/aws4_request"

	headers := map[string][]string{"host": {host}}
	for name, values := range signed {
		lower := strings.ToLower(name)
		headers[lower] = append(headers[lower], values...)
	}
	names := slices.Sorted(maps.Keys(headers))
	var canonicalHeaders strings.Builder
	for _, name := range names {
		values := make([]string, len(headers[name]))
		for i, v := range headers[name] {
			values[i] = strings.Join(strings.Fields(v), " ")
		}
		canonicalHeaders.WriteString(name + ":" + strings.Join(values, ",") + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	request := strings.Join([]string{method, awsPath(rawPath, a.Service == "s3"), awsQuery(rawQuery),
		canonicalHeaders.String(), signedHeaders, payload}, "\n")
	digest := sha256.Sum256([]byte(request))
	toSign := "AWS4-HMAC-SHA256\n" + t.Format("20060102T150405Z") + "\n" + scope + "\n" + hex.EncodeToString(digest[:])

	key := []byte("AWS4" + a.SecretKey)
	for _, part := range []string{date, cmp.Or(a.Region, "us-east-1"), a.Service, "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	return "AWS4-HMAC-SHA256 Credential=" + a.AccessKey + "/" + scope + ", SignedHeaders=" + signedHeaders +
		", Signature=" + hex.EncodeToString(hmacSHA256(key, toSign))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// awsPath is the canonical URI of a path as sent. S3 encodes each decoded
// segment once and keeps the path as it is; other services remove dot
// segments and repeated slashes, then encode the path as sent again.
func awsPath(raw string, s3 bool) string {
	if raw == "" {
		raw = "/"
	}
	if !s3 {
		clean := path.Clean(raw)
		if strings.HasSuffix(raw, "/") && clean != "/" {
			clean += "/"
		}
		raw = clean
	}
	segments := strings.Split(raw, "/")
	for i, segment := range segments {
		if s3 {
			if decoded, err := url.PathUnescape(segment); err == nil {
				segment = decoded
			}
		}
		segments[i] = awsEscape(segment)
	}
	return strings.Join(segments, "/")
}

// awsQuery is the canonical query string: each name and value decoded, then
// encoded the AWS way, sorted by name and then value.
func awsQuery(raw string) string {
	var pairs [][2]string
	for pair := range strings.SplitSeq(raw, "&") {
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		pairs = append(pairs, [2]string{awsEscape(name), awsEscape(value)})
	}
	slices.SortFunc(pairs, func(a, b [2]string) int {
		return cmp.Or(strings.Compare(a[0], b[0]), strings.Compare(a[1], b[1]))
	})
	encoded := make([]string, len(pairs))
	for i, pair := range pairs {
		encoded[i] = pair[0] + "=" + pair[1]
	}
	return strings.Join(encoded, "&")
}

// awsEscape percent-encodes every byte but the unreserved A-Z a-z 0-9 - _ . ~,
// in upper-case hex, as AWS signs.
func awsEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
