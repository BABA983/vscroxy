package request

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func asapTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func decodeASAPToken(t *testing.T, token string) (map[string]any, []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not a compact JWS", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	return claims, signature
}

func TestASAPSignsTheRequiredClaims(t *testing.T) {
	key, keyPEM := asapTestKey(t)
	now := time.Unix(1_700_000_000, 0)
	r := Request{Method: "GET", URL: "https://api.example.com/", Auth: Auth{Type: AuthASAP, ASAP: ASAPAuth{
		Issuer: "svc.example.com", Audience: "api.example.com", KeyID: "key-1", PrivateKey: keyPEM, Expiry: 60,
	}}}
	req, err := r.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := strings.CutPrefix(req.Get("Authorization"), "Bearer ")
	if !ok {
		t.Fatalf("Authorization = %q", req.Get("Authorization"))
	}
	claims, signature := decodeASAPToken(t, token)
	if claims["iss"] != "svc.example.com" || claims["aud"] != "api.example.com" || claims["sub"] != "svc.example.com" {
		t.Fatalf("claims = %+v", claims)
	}
	if claims["kid"] != "key-1" {
		t.Fatalf("kid = %v", claims["kid"])
	}
	if claims["jti"] == "" || claims["iat"] == nil || claims["exp"] == nil {
		t.Fatalf("generated claims = %+v", claims)
	}
	// The signature covers the header and payload.
	parts := strings.Split(token, ".")
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature: %v", err)
	}
	// The generated timestamps use the send clock; they are within a minute
	// of the test's now, not the fixture's.
	iat, _ := claims["iat"].(float64)
	if iat < float64(now.Unix())-60 {
		t.Fatalf("iat = %v", claims["iat"])
	}
}

func TestASAPClaimsOverrideTheFields(t *testing.T) {
	_, keyPEM := asapTestKey(t)
	r := Request{Method: "GET", URL: "https://api.example.com/", Auth: Auth{Type: AuthASAP, ASAP: ASAPAuth{
		Issuer: "field-issuer", Audience: "field-audience", KeyID: "field-key", PrivateKey: keyPEM,
		Claims: `{"iss":"claim-issuer","aud":"claim-audience","kid":"claim-key","sub":"user-1","exp":2000000000,"custom":true}`,
	}}}
	req, err := r.Prepare(nil)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(req.Get("Authorization"), "Bearer ")
	claims, _ := decodeASAPToken(t, token)
	if claims["iss"] != "claim-issuer" || claims["aud"] != "claim-audience" || claims["kid"] != "claim-key" || claims["sub"] != "user-1" {
		t.Fatalf("claims = %+v", claims)
	}
	if claims["exp"] != float64(2000000000) || claims["custom"] != true {
		t.Fatalf("claim overrides = %+v", claims)
	}
}

func TestASAPAcceptsPostmanKeyForms(t *testing.T) {
	_, keyPEM := asapTestKey(t)
	block, _ := pem.Decode([]byte(keyPEM))
	dataURI := "data:application/pkcs8;kid=key-1;base64," + base64.StdEncoding.EncodeToString(block.Bytes)
	base64DER := base64.StdEncoding.EncodeToString(block.Bytes)
	for name, key := range map[string]string{"data URI": dataURI, "base64 DER": base64DER} {
		r := Request{Method: "GET", URL: "https://api.example.com/", Auth: Auth{Type: AuthASAP, ASAP: ASAPAuth{
			Issuer: "svc", Audience: "api", KeyID: "key-1", PrivateKey: key,
		}}}
		if _, err := r.Prepare(nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestASAPRequiresItsClaimsAndKey(t *testing.T) {
	_, keyPEM := asapTestKey(t)
	for name, auth := range map[string]ASAPAuth{
		"issuer":      {Audience: "api", KeyID: "k", PrivateKey: keyPEM},
		"audience":    {Issuer: "svc", KeyID: "k", PrivateKey: keyPEM},
		"key ID":      {Issuer: "svc", Audience: "api", PrivateKey: keyPEM},
		"private key": {Issuer: "svc", Audience: "api", KeyID: "k"},
	} {
		r := Request{Method: "GET", URL: "https://api.example.com/", Auth: Auth{Type: AuthASAP, ASAP: auth}}
		if _, err := r.Prepare(nil); err == nil {
			t.Errorf("%s: missing configuration accepted", name)
		}
	}
}
