package request

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
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
	"$randomIPV6": func() string {
		var address [16]byte
		for i := range address {
			address[i] = byte(rand.IntN(256))
		}
		return netip.AddrFrom16(address).String()
	},
	"$randomMACAddress": func() string {
		return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256))
	},
	"$randomProtocol": func() string { return randomChoice("http", "https") },
	"$randomSemver": func() string {
		return fmt.Sprintf("%d.%d.%d", rand.IntN(10), rand.IntN(10), rand.IntN(10))
	},
	"$randomUserName": func() string { return "user_" + randomText(8, lowercase) },
	"$randomEmail":    func() string { return randomText(10, lowercase) + "@example.com" },
	"$randomPassword": func() string { return randomText(15, alphanumeric) },
	"$randomExampleEmail": func() string {
		return randomText(10, lowercase) + "@example." + randomChoice("com", "net", "org")
	},
	"$randomDomainName":   randomDomainName,
	"$randomDomainSuffix": func() string { return randomChoice("com", "net", "org") },
	"$randomDomainWord":   func() string { return randomText(10, lowercase) },
	"$randomUrl":          func() string { return randomChoice("http", "https") + "://" + randomDomainName() },
	"$randomFirstName":    func() string { return randomChoice(firstNames...) },
	"$randomLastName":     func() string { return randomChoice(lastNames...) },
	"$randomFullName": func() string {
		return randomChoice(firstNames...) + " " + randomChoice(lastNames...)
	},
	"$randomPhoneNumber": func() string {
		return randomText(3, digits) + "-" + randomText(3, digits) + "-" + randomText(4, digits)
	},
	"$randomCountryCode": func() string { return randomChoice("AU", "BR", "CA", "CN", "DE", "FR", "GB", "IN", "JP", "US") },
	"$randomLatitude": func() string {
		return fmt.Sprintf("%.4f", float64(rand.IntN(1_800_001)-900_000)/10_000)
	},
	"$randomLongitude": func() string {
		return fmt.Sprintf("%.4f", float64(rand.IntN(3_600_001)-1_800_000)/10_000)
	},
	"$randomPrice": func() string {
		cents := rand.IntN(100_001)
		return fmt.Sprintf("%d.%02d", cents/100, cents%100)
	},
	"$randomColor": func() string {
		return randomChoice("red", "green", "blue", "yellow", "purple", "orange", "black", "white", "gray", "pink")
	},
	"$randomWeekday": func() string { return time.Weekday(rand.IntN(7)).String() },
	"$randomMonth":   func() string { return time.Month(1 + rand.IntN(12)).String() },
}

const (
	digits       = "0123456789"
	lowercase    = "abcdefghijklmnopqrstuvwxyz0123456789"
	alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// A small, built-in sample vocabulary, not the full Faker dataset.
var (
	firstNames = []string{"Alex", "Amelia", "Daniel", "Emma", "Ethan", "Maya", "Noah", "Olivia", "Sofia", "William"}
	lastNames  = []string{"Brown", "Chen", "Garcia", "Khan", "Kim", "Martin", "Patel", "Silva", "Smith", "Taylor"}
)

func randomChoice(values ...string) string { return values[rand.IntN(len(values))] }

// Keep generated destinations on reserved example domains, as randomEmail does.
func randomDomainName() string {
	return randomText(10, lowercase) + ".example." + randomChoice("com", "net", "org")
}

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
	fields := []*string{&r.Method, &r.URL, &r.Headers, &r.Body, &r.BodyFile, &r.Auth.Token, &r.Auth.Username, &r.Auth.Password, &r.Auth.Key, &r.Auth.Value}
	fields = append(fields, &r.GraphQL.Query, &r.GraphQL.Variables)
	if r.Protocol == "Socket.IO" {
		fields = append(fields, &r.SocketIO.Namespace, &r.SocketIO.Auth, &r.SocketIO.Event, &r.SocketIO.Attachments)
	}
	fields = append(fields, r.Auth.AWS.fields()...)
	fields = append(fields, r.Auth.JWT.fields()...)
	fields = append(fields, r.Auth.OAuth1.fields()...)
	fields = append(fields, r.Auth.Hawk.fields()...)
	fields = append(fields, r.Auth.EdgeGrid.fields()...)
	for _, s := range fields {
		expand(s)
	}
	r.Form = append([]Field(nil), r.Form...)
	for i := range r.Form {
		expand(&r.Form[i].Name)
		expand(&r.Form[i].Value)
	}
	return r
}
