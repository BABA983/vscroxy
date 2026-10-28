package request

import (
	"cmp"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "crypto/sha256" // The JWT hashes, registered for crypto.Hash.
	_ "crypto/sha512"
)

// AuthJWT is the Auth type that signs a JSON Web Token for each send.
const AuthJWT = "JWT Bearer"

// JWTAlgorithms lists the algorithms a token can be signed with, the
// default first.
var JWTAlgorithms = []string{
	"HS256", "HS384", "HS512",
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
}

// JWTAuth is how a request signs its token. Secret, PrivateKey, Payload,
// Header, Prefix and QueryParam may use {{variables}}.
type JWTAuth struct {
	// Algorithm is one of JWTAlgorithms, empty for HS256.
	Algorithm string `json:"algorithm,omitempty"`
	// Secret is the HS* key; SecretBase64 says it is base64-encoded bytes.
	Secret       string `json:"secret,omitempty"`
	SecretBase64 bool   `json:"secretBase64,omitzero"`
	// PrivateKey is the RS*, PS* or ES* key as PEM: PKCS #8, or PKCS #1 for
	// RSA and SEC 1 for EC.
	PrivateKey string `json:"privateKey,omitempty"`
	// Payload is the claims as a JSON object, empty for {}.
	Payload string `json:"payload,omitempty"`
	// Header holds extra JOSE header fields as a JSON object; alg and typ
	// are always the token's own.
	Header string `json:"header,omitempty"`
	// Prefix comes before the token in the Authorization header, empty for
	// Bearer.
	Prefix string `json:"prefix,omitempty"`
	// AddTo is "Query" to send the token as the QueryParam query
	// parameter (empty for token) instead of in the Authorization header.
	AddTo      string `json:"addTo,omitempty"`
	QueryParam string `json:"queryParam,omitempty"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (j *JWTAuth) fields() []*string {
	return []*string{&j.Secret, &j.PrivateKey, &j.Payload, &j.Header, &j.Prefix, &j.QueryParam}
}

func (j JWTAuth) expand(vars map[string]string) JWTAuth {
	for _, f := range j.fields() {
		*f = ExpandVars(*f, vars)
	}
	return j
}

// apply signs a token and adds it to req as configured.
func (j JWTAuth) apply(req *Prepared) error {
	token, err := j.token()
	if err != nil {
		return err
	}
	if j.AddTo != "Query" {
		req.Set("Authorization", cmp.Or(strings.TrimSpace(j.Prefix), "Bearer")+" "+token)
		return nil
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return err
	}
	q.Set(cmp.Or(strings.TrimSpace(j.QueryParam), "token"), token)
	u.RawQuery = q.Encode()
	req.URL = u.String()
	return nil
}

// token is the signed compact JWS of the expanded configuration.
func (j JWTAuth) token() (string, error) {
	alg := cmp.Or(strings.TrimSpace(j.Algorithm), "HS256")
	var hash crypto.Hash
	switch alg[min(len(alg), 2):] {
	case "256":
		hash = crypto.SHA256
	case "384":
		hash = crypto.SHA384
	case "512":
		hash = crypto.SHA512
	}
	family := alg[:min(len(alg), 2)]
	if hash == 0 || family != "HS" && family != "RS" && family != "PS" && family != "ES" {
		return "", fmt.Errorf("JWT algorithm %q is not supported; use one of %s", alg, strings.Join(JWTAlgorithms, ", "))
	}
	payload, err := jwtObject(cmp.Or(strings.TrimSpace(j.Payload), "{}"), "payload")
	if err != nil {
		return "", err
	}
	header, err := jwtHeader(alg, j.Header)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	h := hash.New()
	h.Write([]byte(input))
	digest := h.Sum(nil)

	var signature []byte
	if family == "HS" {
		key := []byte(j.Secret)
		if j.SecretBase64 {
			if key, err = decodeBase64(strings.TrimSpace(j.Secret)); err != nil {
				return "", errors.New("the JWT secret is not valid base64")
			}
		}
		mac := hmac.New(hash.New, key)
		mac.Write([]byte(input))
		signature = mac.Sum(nil)
	} else {
		key, err := jwtPrivateKey(j.PrivateKey)
		if err != nil {
			return "", err
		}
		switch k := key.(type) {
		case *rsa.PrivateKey:
			if family == "RS" {
				signature, err = rsa.SignPKCS1v15(rand.Reader, k, hash, digest)
			} else if family == "PS" {
				signature, err = rsa.SignPSS(rand.Reader, k, hash, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			} else {
				err = fmt.Errorf("%s needs an EC private key, not an RSA one", alg)
			}
		case *ecdsa.PrivateKey:
			curve := map[string]elliptic.Curve{"256": elliptic.P256(), "384": elliptic.P384(), "512": elliptic.P521()}[alg[2:]]
			if family != "ES" {
				err = fmt.Errorf("%s needs an RSA private key, not an EC one", alg)
			} else if k.Curve != curve {
				err = fmt.Errorf("%s needs a key on curve %s, not %s", alg, curve.Params().Name, k.Curve.Params().Name)
			} else {
				signature, err = ecdsaJWS(k, digest)
			}
		default:
			err = fmt.Errorf("%s needs an RSA or EC private key", alg)
		}
		if err != nil {
			return "", err
		}
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// jwtObject is text as compact JSON, which must be an object. Its members
// keep their order.
func jwtObject(text, what string) ([]byte, error) {
	v := jsontext.Value(text)
	if err := v.Compact(); err != nil || v.Kind() != '{' {
		return nil, fmt.Errorf("the JWT %s must be a JSON object", what)
	}
	return v, nil
}

// jwtHeader is the JOSE header: alg and typ, then the extra fields but
// those two, in their order.
func jwtHeader(alg, extra string) ([]byte, error) {
	header := []byte(`{"alg":"` + alg + `","typ":"JWT"`)
	if strings.TrimSpace(extra) != "" {
		object, err := jwtObject(extra, "header")
		if err != nil {
			return nil, err
		}
		dec := jsontext.NewDecoder(strings.NewReader(string(object)))
		dec.ReadToken() // {
		for dec.PeekKind() != '}' {
			token, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := token.String() // Before the next read voids the token.
			value, err := dec.ReadValue()
			if err != nil {
				return nil, err
			}
			if name == "alg" || name == "typ" {
				continue
			}
			key, _ := jsontext.AppendQuote(nil, name)
			header = append(append(append(append(header, ','), key...), ':'), value...)
		}
	}
	return append(header, '}'), nil
}

// decodeBase64 reads standard or URL-safe base64, padded or not.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// jwtPrivateKey parses the first PEM block of text: PKCS #8, PKCS #1 or SEC 1.
func jwtPrivateKey(text string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(text)))
	if block == nil {
		return nil, errors.New("the JWT private key must be PEM, starting with -----BEGIN PRIVATE KEY-----")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("the JWT private key (%s) is not a PKCS #8, PKCS #1 or SEC 1 private key", block.Type)
}

// ecdsaJWS signs digest as JWS wants: r and s as fixed-size big-endian
// integers, one after the other (RFC 7518 §3.4).
func ecdsaJWS(k *ecdsa.PrivateKey, digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, k, digest)
	if err != nil {
		return nil, err
	}
	size := (k.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, 2*size)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signature, nil
}
