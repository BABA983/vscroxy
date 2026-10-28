package request

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// The fields of the active auth type are variables a send unlocks and
// generates, and only those.
func TestAuthTypesVariables(t *testing.T) {
	auth := Auth{Username: "{{u}}", Password: "{{vault:p}}",
		AWS: AWSAuth{AccessKey: "{{a1}}", SecretKey: "{{vault:a2}}", Region: "{{a3}}", Service: "{{a4}}", SessionToken: "{{a5}}"},
		JWT: JWTAuth{Algorithm: "{{x}}", Secret: "{{vault:j1}}", PrivateKey: "{{j2}}", Payload: `{"n":"{{j3}}"}`, Header: "{{j4}}", Prefix: "{{j5}}", AddTo: "{{x}}", QueryParam: "{{j6}}"}}
	for typ, want := range map[string][]string{
		AuthDigest: {"u", "vault:p"},
		AuthAWS:    {"a1", "a3", "a4", "a5", "vault:a2"},
		AuthJWT:    {"j2", "j3", "j4", "j5", "j6", "vault:j1"},
	} {
		auth.Type = typ
		if got := (Request{Auth: auth}).Variables(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, want %q", typ, got, want)
		}
	}
	r := Request{Auth: Auth{AWS: AWSAuth{SessionToken: "{{$randomInt}}"}, JWT: JWTAuth{Payload: `{"jti":"{{$guid}}"}`}}}.withDynamic(nil)
	if strings.Contains(r.Auth.AWS.SessionToken, "{{") || strings.Contains(r.Auth.JWT.Payload, "{{") {
		t.Errorf("dynamic variables left: %+v", r.Auth)
	}
}

// jwt.io's example token, signed with its default secret.
func TestJWTKnownToken(t *testing.T) {
	r := Request{Method: "GET", URL: "https://api.example.com/me", Auth: Auth{Type: AuthJWT, JWT: JWTAuth{
		Secret:  "{{secret}}",
		Payload: "{\n  \"sub\": \"{{sub}}\",\n  \"name\": \"John Doe\",\n  \"iat\": 1516239022\n}",
	}}}
	req, err := r.Prepare(map[string]string{"secret": "your-256-bit-secret", "sub": "1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	if got := req.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func pemKey(t *testing.T, kind string, der []byte, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

func TestJWTSignsEveryAlgorithm(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	rsaPEMs := []string{pemKey(t, "PRIVATE KEY", pkcs8, err), pemKey(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey), nil)}
	ecKeys := map[string]*ecdsa.PrivateKey{}
	ecPEMs := map[string][]string{}
	for size, curve := range map[string]elliptic.Curve{"256": elliptic.P256(), "384": elliptic.P384(), "512": elliptic.P521()} {
		k, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		ecKeys[size] = k
		pkcs8, err := x509.MarshalPKCS8PrivateKey(k)
		sec1, err1 := x509.MarshalECPrivateKey(k)
		ecPEMs[size] = []string{pemKey(t, "PRIVATE KEY", pkcs8, err), pemKey(t, "EC PRIVATE KEY", sec1, err1)}
	}
	hashes := map[string]crypto.Hash{"256": crypto.SHA256, "384": crypto.SHA384, "512": crypto.SHA512}
	secret := []byte("a secret\x00with any bytes")

	for _, alg := range JWTAlgorithms {
		size := alg[2:]
		hash := hashes[size]
		var configs []JWTAuth
		switch alg[:2] {
		case "HS":
			configs = []JWTAuth{
				{Algorithm: alg, Secret: string(secret)},
				{Algorithm: alg, Secret: base64.StdEncoding.EncodeToString(secret), SecretBase64: true},
			}
		case "RS", "PS":
			for _, key := range rsaPEMs {
				configs = append(configs, JWTAuth{Algorithm: alg, PrivateKey: key})
			}
		case "ES":
			for _, key := range ecPEMs[size] {
				configs = append(configs, JWTAuth{Algorithm: alg, PrivateKey: key})
			}
		}
		for i, config := range configs {
			config.Payload, config.Header = `{"iss": "vscroxy", "n": 1}`, `{"kid": "k1", "alg": "none", "typ": "x"}`
			token, err := config.token()
			if err != nil {
				t.Fatalf("%s #%d: %v", alg, i, err)
			}
			parts := strings.Split(token, ".")
			if len(parts) != 3 {
				t.Fatalf("%s: token %q", alg, token)
			}
			header, _ := base64.RawURLEncoding.DecodeString(parts[0])
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			if string(header) != `{"alg":"`+alg+`","typ":"JWT","kid":"k1"}` || string(payload) != `{"iss":"vscroxy","n":1}` {
				t.Errorf("%s: header %s, payload %s", alg, header, payload)
			}
			signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
			input := []byte(parts[0] + "." + parts[1])
			h := hash.New()
			h.Write(input)
			digest := h.Sum(nil)
			var ok bool
			switch alg[:2] {
			case "HS":
				mac := hmac.New(hash.New, secret)
				mac.Write(input)
				ok = hmac.Equal(signature, mac.Sum(nil))
			case "RS":
				ok = rsa.VerifyPKCS1v15(&rsaKey.PublicKey, hash, digest, signature) == nil
			case "PS":
				ok = rsa.VerifyPSS(&rsaKey.PublicKey, hash, digest, signature, &rsa.PSSOptions{SaltLength: hash.Size()}) == nil
			case "ES":
				k := ecKeys[size]
				n := (k.Curve.Params().BitSize + 7) / 8
				ok = len(signature) == 2*n && ecdsa.Verify(&k.PublicKey, digest, new(big.Int).SetBytes(signature[:n]), new(big.Int).SetBytes(signature[n:]))
			}
			if !ok {
				t.Errorf("%s #%d: the signature does not verify", alg, i)
			}
		}
	}
}

func TestJWTPlacementAndErrors(t *testing.T) {
	r := Request{Method: "GET", URL: "https://api.example.com/me?a=1", Auth: Auth{Type: AuthJWT, JWT: JWTAuth{
		Secret: "s", Prefix: "{{prefix}}", AddTo: "Query", QueryParam: "{{param}}",
	}}}
	req, err := r.Prepare(map[string]string{"prefix": "JWT", "param": "access"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	if token := u.Query().Get("access"); u.Query().Get("a") != "1" || strings.Count(token, ".") != 2 || req.Get("Authorization") != "" {
		t.Errorf("query %q, Authorization %q", u.RawQuery, req.Get("Authorization"))
	}
	r.Auth.JWT.QueryParam = ""
	if req, _ = r.Prepare(nil); !strings.Contains(req.URL, "token=") {
		t.Errorf("default query parameter: %s", req.URL)
	}
	r.Auth.JWT.AddTo = ""
	if req, _ = r.Prepare(map[string]string{"prefix": "JWT"}); !strings.HasPrefix(req.Get("Authorization"), "JWT ey") {
		t.Errorf("Authorization = %q", req.Get("Authorization"))
	}
	// An empty payload is {}.
	token, _ := JWTAuth{Secret: "s"}.token()
	var claims map[string]any
	payload, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if json.Unmarshal(payload, &claims) != nil || len(claims) != 0 {
		t.Errorf("empty payload = %s", payload)
	}

	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	ec := pemKey(t, "PRIVATE KEY", der, err)
	for _, tt := range []struct {
		config JWTAuth
		want   string
	}{
		{JWTAuth{Algorithm: "none"}, "not supported"},
		{JWTAuth{Algorithm: "EdDSA"}, "not supported"},
		{JWTAuth{Payload: `["a"]`}, "payload must be a JSON object"},
		{JWTAuth{Payload: `{"a": {{n}}}`}, "payload must be a JSON object"},
		{JWTAuth{Header: `kid`}, "header must be a JSON object"},
		{JWTAuth{Secret: "not base64!", SecretBase64: true}, "not valid base64"},
		{JWTAuth{Algorithm: "RS256"}, "must be PEM"},
		{JWTAuth{Algorithm: "RS256", PrivateKey: ec}, "needs an RSA private key"},
		{JWTAuth{Algorithm: "ES384", PrivateKey: ec}, "curve P-384"},
	} {
		if _, err := tt.config.token(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: %v, want %q", tt.config, err, tt.want)
		}
	}
}
