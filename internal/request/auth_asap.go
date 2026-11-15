package request

import (
	"cmp"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuthASAP is Atlassian's Service Authentication Protocol: a short-lived JWT
// the client signs itself and sends as a Bearer token.
const AuthASAP = "Atlassian ASAP"

// ASAPAlgorithms lists the asymmetric algorithms ASAP accepts, the default
// first. Symmetric algorithms are not part of the protocol.
var ASAPAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
}

// ASAPAuth is how a request signs its ASAP token. Every text may use
// {{variables}}.
type ASAPAuth struct {
	// Algorithm is one of ASAPAlgorithms, empty for RS256.
	Algorithm string `json:"algorithm,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
	Audience  string `json:"audience,omitempty"`
	KeyID     string `json:"keyId,omitempty"`
	// PrivateKey is PEM (PKCS #8, PKCS #1 or SEC 1), a Postman data URI, or
	// base64 DER.
	PrivateKey string `json:"privateKey,omitempty"`
	Subject    string `json:"subject,omitempty"`
	// Claims is an extra claims object as JSON; its members override the
	// fields above and exp is an absolute timestamp.
	Claims string `json:"claims,omitempty"`
	// Expiry is the token lifetime in seconds, empty for 3600.
	Expiry int `json:"expiry,omitzero"`
}

// fields are the configuration's texts, for expansion and variable lists.
func (a *ASAPAuth) fields() []*string {
	return []*string{&a.Algorithm, &a.Issuer, &a.Audience, &a.KeyID, &a.PrivateKey, &a.Subject, &a.Claims}
}

func (a ASAPAuth) expand(vars map[string]string) ASAPAuth {
	for _, f := range a.fields() {
		*f = ExpandVars(*f, vars)
	}
	return a
}

// apply signs an ASAP token and adds it to req as a Bearer token.
func (a ASAPAuth) apply(req *Prepared, at time.Time) error {
	alg := cmp.Or(strings.TrimSpace(a.Algorithm), ASAPAlgorithms[0])
	if !slices.Contains(ASAPAlgorithms, alg) {
		return fmt.Errorf("ASAP algorithm %q is not supported; use one of %s", alg, strings.Join(ASAPAlgorithms, ", "))
	}
	claims := map[string]any{}
	if text := strings.TrimSpace(a.Claims); text != "" {
		if err := json.Unmarshal([]byte(text), &claims); err != nil {
			return fmt.Errorf("ASAP claims must be a JSON object: %w", err)
		}
	}
	stringClaim := func(name string) (string, error) {
		value, ok := claims[name]
		if !ok {
			return "", nil
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("ASAP claim %q must be a string", name)
		}
		return text, nil
	}
	issuer, err := stringClaim("iss")
	if err != nil {
		return err
	}
	issuer = cmp.Or(strings.TrimSpace(issuer), strings.TrimSpace(a.Issuer))
	audience := strings.TrimSpace(a.Audience)
	if value, ok := claims["aud"]; ok {
		switch v := value.(type) {
		case string:
			audience = strings.TrimSpace(v)
		case []any:
			if len(v) == 0 {
				return errors.New("the ASAP audience claim is empty")
			}
		default:
			return errors.New("the ASAP audience claim must be a string or an array of strings")
		}
	}
	kid, err := stringClaim("kid")
	if err != nil {
		return err
	}
	kid = cmp.Or(strings.TrimSpace(kid), strings.TrimSpace(a.KeyID))
	subject, err := stringClaim("sub")
	if err != nil {
		return err
	}
	subject = cmp.Or(strings.TrimSpace(subject), strings.TrimSpace(a.Subject), issuer)
	if issuer == "" || audience == "" || kid == "" || strings.TrimSpace(a.PrivateKey) == "" {
		return errors.New("ASAP needs an issuer, an audience, a key ID and a private key")
	}
	if _, ok := claims["jti"]; !ok {
		claims["jti"] = uuid.NewString()
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = at.Unix()
	}
	if _, ok := claims["exp"]; !ok {
		expiry := a.Expiry
		if expiry <= 0 {
			expiry = 3600
		}
		claims["exp"] = at.Unix() + int64(expiry)
	}
	claims["iss"] = issuer
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = audience
	}
	claims["kid"] = kid
	claims["sub"] = subject
	payload, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("encode ASAP claims: %w", err)
	}
	header, err := json.Marshal(map[string]string{"kid": kid})
	if err != nil {
		return err
	}
	key, err := asapPrivateKey(a.PrivateKey, kid)
	if err != nil {
		return err
	}
	token, err := JWTAuth{Algorithm: alg, PrivateKey: key, Payload: string(payload), Header: string(header)}.token()
	if err != nil {
		return fmt.Errorf("sign ASAP token: %w", err)
	}
	req.Set("Authorization", "Bearer "+token)
	return nil
}

// asapPrivateKey accepts the key forms Postman does: PEM, a
// data:application/pkcs8;kid=...;base64,... URI, or bare base64 DER.
func asapPrivateKey(raw, kid string) (string, error) {
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(raw, "data:application/pkcs8;kid="); ok {
		uriKID, encoded, ok := strings.Cut(rest, ";base64,")
		if !ok || uriKID != kid {
			return "", errors.New("the ASAP private key data URI has a different key ID")
		}
		der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return "", errors.New("the ASAP private key data URI is not valid base64")
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return raw, nil
	}
	der, err := decodeBase64(raw)
	if err == nil {
		if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
			return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
		}
	}
	return "", errors.New("the ASAP private key must be PEM, a PKCS #8 data URI, or base64 DER")
}
