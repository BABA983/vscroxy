package request

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"uuid"
)

// dynamic are the {{$name}} variables a send generates afresh at each
// occurrence, with Postman's names, so imported collections keep working.
var dynamic = map[string]func() string{
	"$guid":       func() string { return uuid.New().String() },
	"$randomUUID": func() string { return uuid.New().String() },
	"$timestamp":  func() string { return strconv.FormatInt(time.Now().Unix(), 10) },
	"$isoTimestamp": func() string {
		return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	},
	"$randomInt":          func() string { return strconv.Itoa(rand.IntN(1001)) },
	"$randomBoolean":      func() string { return strconv.FormatBool(rand.IntN(2) == 1) },
	"$randomAlphaNumeric": func() string { return randomText(1, alphanumeric) },
	"$randomHexColor":     func() string { return fmt.Sprintf("#%06x", rand.IntN(1<<24)) },
	"$randomIP": func() string {
		return fmt.Sprintf("%d.%d.%d.%d", 1+rand.IntN(223), rand.IntN(256), rand.IntN(256), 1+rand.IntN(254))
	},
	"$randomUserName": func() string { return "user_" + randomText(8, lowercase) },
	"$randomEmail":    func() string { return randomText(10, lowercase) + "@example.com" },
	"$randomPassword": func() string { return randomText(15, alphanumeric) },
}

const (
	lowercase    = "abcdefghijklmnopqrstuvwxyz0123456789"
	alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

func randomText(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return string(b)
}

// expandDynamic generates each {{$name}} occurrence a defined variable does
// not shadow, leaving every other reference for ExpandVars.
func expandDynamic(s string, defined map[string]string) string {
	if !strings.Contains(s, "{{$") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		end := -1
		if start >= 0 {
			end = strings.Index(s[start:], "}}")
		}
		if start < 0 || end < 0 {
			b.WriteString(s)
			return b.String()
		}
		name := strings.TrimSpace(s[start+2 : start+end])
		b.WriteString(s[:start])
		if _, shadowed := defined[name]; !shadowed && dynamic[name] != nil {
			b.WriteString(dynamic[name]())
		} else {
			b.WriteString(s[start : start+end+2])
		}
		s = s[start+end+2:]
	}
}

// withDynamic generates the dynamic variables in every authored field.
func (r Request) withDynamic(defined map[string]string) Request {
	expand := func(s *string) { *s = expandDynamic(*s, defined) }
	for _, s := range []*string{&r.Method, &r.URL, &r.Headers, &r.Body, &r.Auth.Token, &r.Auth.Username, &r.Auth.Password, &r.Auth.Key, &r.Auth.Value} {
		expand(s)
	}
	r.Form = append([]Field(nil), r.Form...)
	for i := range r.Form {
		expand(&r.Form[i].Name)
		expand(&r.Form[i].Value)
	}
	return r
}
