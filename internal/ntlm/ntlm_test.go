package ntlm

import (
	"encoding/base64"
	"testing"
)

// type2Message is a minimal NTLM Type 2 message: signature, type, and a
// challenge. The parser only needs the first 12 bytes to recognize it.
func type2Message() []byte {
	message := make([]byte, 16)
	copy(message, "NTLMSSP\x00")
	message[8] = 2
	return message
}

func TestProxyChallengePicksTheUsableToken(t *testing.T) {
	type2 := base64.StdEncoding.EncodeToString(type2Message())
	kerberos := base64.StdEncoding.EncodeToString([]byte("not an NTLM message"))
	for _, test := range []struct {
		name      string
		values    []string
		scheme    string
		wantType2 bool
		offered   bool
	}{
		{"negotiate token first", []string{"Negotiate " + type2 + ", NTLM"}, SchemeNegotiate, true, true},
		{"ntlm token", []string{"NTLM " + type2}, SchemeNTLM, true, true},
		{"bare ntlm wins over bare negotiate", []string{"Negotiate, NTLM"}, SchemeNTLM, false, true},
		{"bare negotiate", []string{"Negotiate"}, SchemeNegotiate, false, true},
		{"kerberos token becomes a bare negotiate probe", []string{"Negotiate " + kerberos}, SchemeNegotiate, false, true},
		{"other scheme only", []string{`Basic realm="proxy"`}, "", false, false},
		{"quoted scheme name is not a challenge", []string{`Basic realm="NTLM"`}, "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			scheme, token, offered := ProxyChallenge(test.values)
			if offered != test.offered || scheme != test.scheme || (len(token) > 0) != test.wantType2 {
				t.Fatalf("ProxyChallenge = %q, %x, %t; want %q, type2=%t, offered=%t",
					scheme, token, offered, test.scheme, test.wantType2, test.offered)
			}
		})
	}
}

func TestChallengeStillOnlyReadsNTLM(t *testing.T) {
	type2 := base64.StdEncoding.EncodeToString(type2Message())
	if _, offered := Challenge([]string{"Negotiate " + type2}); offered {
		t.Fatal("Challenge accepted a Negotiate-only challenge")
	}
	got, offered := Challenge([]string{"Negotiate " + type2 + ", NTLM " + type2})
	if !offered || len(got) == 0 {
		t.Fatalf("Challenge = %x, %t", got, offered)
	}
}
