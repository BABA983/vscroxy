package request

import (
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// documentedDynamicNames reads the Postman list kept in testdata. The test
// deliberately does not derive names from the dynamic map.
func documentedDynamicNames(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "postman-dynamic-variables.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	slices.Sort(names)
	return names
}

func TestDynamicNamesMatchPostmanList(t *testing.T) {
	documented := documentedDynamicNames(t)
	if len(documented) != 118 {
		t.Fatalf("testdata has %d names, want the 118 on Postman's page", len(documented))
	}
	got := DynamicNames()
	if !slices.Equal(got, documented) {
		missing := slices.DeleteFunc(slices.Clone(documented), func(name string) bool { return slices.Contains(got, name) })
		extra := slices.DeleteFunc(slices.Clone(got), func(name string) bool { return slices.Contains(documented, name) })
		t.Fatalf("DynamicNames differs from Postman's list\nmissing: %v\nextra: %v", missing, extra)
	}
}

func TestDynamicValuesMatchPostmanFormats(t *testing.T) {
	documented := documentedDynamicNames(t)
	checks := dynamicFormatChecks()
	for name := range checks {
		if !slices.Contains(documented, name) {
			t.Errorf("format check for undocumented variable %s", name)
		}
	}
	for _, name := range documented {
		check, ok := checks[name]
		if !ok {
			t.Fatalf("no format check for %s", name)
		}
		for i := range 32 {
			value, ok := DynamicValue(name)
			if !ok {
				t.Fatalf("DynamicValue(%q) was not found", name)
			}
			if value == "" {
				t.Fatalf("DynamicValue(%q) is empty", name)
			}
			if err := check(value); err != nil {
				t.Fatalf("%s sample %d %q: %v", name, i, value, err)
			}
		}
	}
}

// A dynamic variable is resolved once per occurrence, as Postman's
// Substitutor calls each generator for each match.
func TestExpandDynamicGeneratesEveryOccurrence(t *testing.T) {
	const name = "$randomFirstName"
	old := dynamic[name]
	calls := 0
	dynamic[name] = func() string {
		calls++
		return strconv.Itoa(calls)
	}
	t.Cleanup(func() { dynamic[name] = old })

	got := expandDynamic("{{"+name+"}}/{{"+name+"}}", nil)
	if got != "1/2" || calls != 2 {
		t.Fatalf("expanded %q with %d calls, want 1/2 with 2 calls", got, calls)
	}
}

func TestExpandDynamicDefinedNameWinsEvenWhenEmpty(t *testing.T) {
	defined := map[string]string{"$randomFirstName": ""}
	got := ExpandVars(expandDynamic("{{$randomFirstName}}/{{$randomFirstName}}", defined), defined)
	if got != "/" {
		t.Fatalf("expanded to %q, want two empty shadowed values", got)
	}
	if got := expandDynamic("{{$randomNotImplemented}}", nil); got != "{{$randomNotImplemented}}" {
		t.Fatalf("unknown name became %q", got)
	}
}

func dynamicFormatChecks() map[string]func(string) error {
	match := func(pattern string) func(string) error {
		re := regexp.MustCompile(pattern)
		return func(value string) error {
			if !re.MatchString(value) {
				return fmt.Errorf("does not match %s", pattern)
			}
			return nil
		}
	}
	oneOf := func(label string, values ...string) func(string) error {
		set := make(map[string]bool, len(values))
		for _, value := range values {
			set[value] = true
		}
		return func(value string) error {
			if !set[value] {
				return fmt.Errorf("%q is not one of the %s", value, label)
			}
			return nil
		}
	}
	fields := func(min, max int, allowed string) func(string) error {
		re := regexp.MustCompile(allowed)
		return func(value string) error {
			parts := strings.Fields(value)
			if len(parts) < min || max > 0 && len(parts) > max {
				return fmt.Errorf("%q has %d words", value, len(parts))
			}
			if !re.MatchString(value) {
				return fmt.Errorf("%q has unexpected characters", value)
			}
			return nil
		}
	}

	checks := map[string]func(string) error{
		"$guid":         match(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`),
		"$randomUUID":   match(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`),
		"$timestamp":    checkTimestamp,
		"$isoTimestamp": checkISOTimestamp,

		"$randomAlphaNumeric": match(`^[A-Za-z0-9]$`),
		"$randomBoolean":      match(`^(true|false)$`),
		"$randomInt":          checkRandomInt,
		"$randomColor": oneOf("faker colors",
			"red", "green", "blue", "yellow", "purple", "mint green", "teal", "white", "black",
			"orange", "pink", "grey", "maroon", "violet", "turquoise", "tan", "sky blue", "salmon",
			"plum", "orchid", "olive", "magenta", "lime", "ivory", "indigo", "gold", "fuchsia",
			"cyan", "azure", "lavender", "silver"),
		"$randomHexColor": match(`^#[0-9a-f]{6}$`),
		"$randomAbbreviation": oneOf("hacker abbreviations",
			"TCP", "HTTP", "SDD", "RAM", "GB", "CSS", "SSL", "AGP", "SQL", "FTP", "PCI", "AI",
			"ADP", "RSS", "XML", "EXE", "COM", "HDD", "THX", "SMTP", "SMS", "USB", "PNG", "SAS",
			"IB", "SCSI", "JSON", "XSS", "JBOD"),

		"$randomIP":         checkIPv4,
		"$randomIPV6":       checkIPv6,
		"$randomMACAddress": match(`^(?:[0-9a-f]{2}:){5}[0-9a-f]{2}$`),
		"$randomPassword":   match(`^[A-Za-z0-9]{15}$`),
		"$randomLocale":     match(`^[a-z]{2,3}$`),
		"$randomUserAgent":  checkUserAgent,
		"$randomProtocol":   match(`^(http|https)$`),
		"$randomSemver":     match(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`),

		"$randomFirstName":  match(`^[A-Z][A-Za-z' -]+$`),
		"$randomLastName":   match(`^[A-Z][A-Za-z' -]+$`),
		"$randomFullName":   checkFullName,
		"$randomNamePrefix": oneOf("faker name prefixes", "Mr.", "Mrs.", "Ms.", "Miss", "Dr."),
		"$randomNameSuffix": oneOf("faker name suffixes", "Jr.", "Sr.", "I", "II", "III", "IV", "V", "MD", "DDS", "PhD", "DVM"),

		"$randomJobArea":       match(`^[A-Z][A-Za-z]+$`),
		"$randomJobDescriptor": match(`^[A-Z][A-Za-z]+$`),
		"$randomJobTitle":      fields(3, 0, `^[A-Za-z][A-Za-z/ -]+$`),
		"$randomJobType":       match(`^[A-Z][a-z]+$`),

		"$randomPhoneNumber":    match(`^[0-9]{3}-[0-9]{3}-[0-9]{4}$`),
		"$randomPhoneNumberExt": match(`^[0-9]{1,2}-[0-9]{3}-[0-9]{3}-[0-9]{4}$`),
		"$randomCity":           match(`^[A-Z][A-Za-z' .-]+$`),
		"$randomStreetName":     match(`^[A-Z][A-Za-z' -]+ [A-Z][A-Za-z]+$`),
		"$randomStreetAddress":  match(`^[0-9]{3,5} [A-Z][A-Za-z' -]+ [A-Z][A-Za-z]+$`),
		"$randomCountry":        match(`^[A-Z][A-Za-z' .()-]+$`),
		"$randomCountryCode":    match(`^[A-Z]{2}$`),
		"$randomLatitude":       checkLatitude,
		"$randomLongitude":      checkLongitude,

		"$randomAvatarImage":    checkAvatarImage,
		"$randomImageUrl":       match(`^http://placeimg\.com/640/480$`),
		"$randomAbstractImage":  match(`^http://placeimg\.com/640/480/abstract$`),
		"$randomAnimalsImage":   match(`^http://placeimg\.com/640/480/animals$`),
		"$randomBusinessImage":  match(`^http://placeimg\.com/640/480/business$`),
		"$randomCatsImage":      match(`^http://placeimg\.com/640/480/cats$`),
		"$randomCityImage":      match(`^http://placeimg\.com/640/480/city$`),
		"$randomFoodImage":      match(`^http://placeimg\.com/640/480/food$`),
		"$randomNightlifeImage": match(`^http://placeimg\.com/640/480/nightlife$`),
		"$randomFashionImage":   match(`^http://placeimg\.com/640/480/fashion$`),
		"$randomPeopleImage":    match(`^http://placeimg\.com/640/480/people$`),
		"$randomNatureImage":    match(`^http://placeimg\.com/640/480/nature$`),
		"$randomSportsImage":    match(`^http://placeimg\.com/640/480/sports$`),
		"$randomTransportImage": match(`^http://placeimg\.com/640/480/transport$`),
		"$randomImageDataUri":   checkImageDataURI,

		"$randomBankAccount": match(`^[0-9]{8}$`),
		"$randomBankAccountName": oneOf("faker account names",
			"Checking Account", "Savings Account", "Money Market Account", "Investment Account",
			"Home Loan Account", "Credit Card Account", "Auto Loan Account", "Personal Loan Account"),
		"$randomCreditCardMask":  match(`^[0-9]{4}$`),
		"$randomBankAccountBic":  match(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}(?:[A-Z0-9]{3})?$`),
		"$randomBankAccountIban": checkIBAN,
		"$randomTransactionType": oneOf("faker transaction types", "deposit", "withdrawal", "payment", "invoice"),
		"$randomCurrencyCode":    checkCurrencyCode,
		"$randomCurrencyName":    match(`^[A-Za-z][A-Za-z .()-]+$`),
		"$randomCurrencySymbol":  checkCurrencySymbol,
		"$randomBitcoin":         match(`^[13][1-9A-HJ-NP-Za-km-z]{24,33}$`),

		"$randomCompanyName":   checkCompanyName,
		"$randomCompanySuffix": oneOf("faker company suffixes", "Inc", "and Sons", "LLC", "Group"),
		"$randomBs":            fields(3, 0, `^[A-Za-z0-9][A-Za-z0-9/.,' -]+$`),
		"$randomBsAdjective":   match(`^[A-Za-z0-9][A-Za-z0-9/.-]+$`),
		"$randomBsBuzz":        match(`^[a-z][a-z-]+$`),
		"$randomBsNoun":        match(`^[A-Za-z0-9][A-Za-z0-9/ .-]+$`),

		"$randomCatchPhrase":           fields(3, 0, `^[A-Za-z0-9][A-Za-z0-9/.,' -]+$`),
		"$randomCatchPhraseAdjective":  match(`^[A-Z][A-Za-z-]+$`),
		"$randomCatchPhraseDescriptor": match(`^[A-Za-z0-9][A-Za-z0-9/ .-]+$`),
		"$randomCatchPhraseNoun":       match(`^[A-Za-z0-9][A-Za-z0-9/ .-]+$`),

		"$randomDatabaseColumn": oneOf("faker database columns",
			"id", "title", "name", "email", "phone", "token", "group", "category", "password",
			"comment", "avatar", "status", "createdAt", "updatedAt"),
		"$randomDatabaseType": oneOf("faker database types",
			"int", "varchar", "text", "date", "datetime", "tinyint", "time", "timestamp", "smallint",
			"mediumint", "bigint", "decimal", "float", "double", "real", "bit", "boolean", "serial",
			"blob", "binary", "enum", "set", "geometry", "point"),
		"$randomDatabaseCollation": oneOf("faker database collations",
			"utf8_unicode_ci", "utf8_general_ci", "utf8_bin", "ascii_bin", "ascii_general_ci",
			"cp1250_bin", "cp1250_general_ci"),
		"$randomDatabaseEngine": oneOf("faker database engines",
			"InnoDB", "MyISAM", "MEMORY", "CSV", "BLACKHOLE", "ARCHIVE"),

		"$randomDateFuture": func(value string) error { return checkJSDate(value, "future") },
		"$randomDatePast":   func(value string) error { return checkJSDate(value, "past") },
		"$randomDateRecent": func(value string) error { return checkJSDate(value, "recent") },
		"$randomWeekday": oneOf("English weekdays",
			"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"),
		"$randomMonth": oneOf("English months",
			"January", "February", "March", "April", "May", "June", "July", "August", "September",
			"October", "November", "December"),

		"$randomDomainName":   match(`^[a-z]+\.[a-z]+$`),
		"$randomDomainSuffix": oneOf("faker domain suffixes", "com", "biz", "info", "name", "net", "org"),
		"$randomDomainWord":   match(`^[a-z]+$`),
		"$randomEmail":        checkEmail,
		"$randomExampleEmail": checkExampleEmail,
		"$randomUserName":     match(`^[A-Za-z][A-Za-z0-9._]*$`),
		"$randomUrl":          checkURL,

		"$randomFileName": match(`^[a-z0-9_]+(?:\.[a-z0-9]+)+$`),
		"$randomFileType": oneOf("faker file types",
			"application", "audio", "chemical", "font", "image", "message", "model", "multipart",
			"text", "video"),
		"$randomFileExt":        match(`^[a-z0-9]+$`),
		"$randomCommonFileName": match(`^[a-z0-9_]+(?:\.[a-z0-9]+)+$`),
		"$randomCommonFileType": oneOf("faker common file types", "video", "audio", "image", "text", "application"),
		"$randomCommonFileExt":  match(`^[a-z0-9]+$`),
		"$randomFilePath":       checkFilePath,
		"$randomDirectoryPath":  checkDirectoryPath,
		"$randomMimeType":       match(`^[a-z]+/[a-z0-9][a-z0-9.+-]*$`),

		"$randomPrice": checkPrice,
		"$randomProduct": oneOf("faker products",
			"Chair", "Car", "Computer", "Keyboard", "Mouse", "Bike", "Ball", "Gloves", "Pants",
			"Shirt", "Table", "Shoes", "Hat", "Towels", "Soap", "Tuna", "Chicken", "Fish",
			"Cheese", "Bacon", "Pizza", "Salad", "Sausages", "Chips"),
		"$randomProductAdjective": oneOf("faker product adjectives",
			"Small", "Ergonomic", "Rustic", "Intelligent", "Gorgeous", "Incredible", "Fantastic",
			"Practical", "Sleek", "Awesome", "Generic", "Handcrafted", "Handmade", "Licensed",
			"Refined", "Unbranded", "Tasty"),
		"$randomProductMaterial": oneOf("faker product materials",
			"Steel", "Wooden", "Concrete", "Plastic", "Cotton", "Granite", "Rubber", "Metal",
			"Soft", "Fresh", "Frozen"),
		"$randomProductName": fields(3, 0, `^[A-Za-z][A-Za-z ]+$`),
		"$randomDepartment": oneOf("faker departments",
			"Books", "Movies", "Music", "Games", "Electronics", "Computers", "Home", "Garden",
			"Tools", "Grocery", "Health", "Beauty", "Toys", "Kids", "Baby", "Clothing", "Shoes",
			"Jewelery", "Sports", "Outdoors", "Automotive", "Industrial"),

		"$randomNoun": oneOf("hacker nouns",
			"driver", "protocol", "bandwidth", "panel", "microchip", "program", "port", "card",
			"array", "interface", "system", "sensor", "firewall", "hard drive", "pixel", "alarm",
			"feed", "monitor", "application", "transmitter", "bus", "circuit", "capacitor", "matrix"),
		"$randomVerb": oneOf("hacker verbs",
			"back up", "bypass", "hack", "override", "compress", "copy", "navigate", "index",
			"connect", "generate", "quantify", "calculate", "synthesize", "input", "transmit",
			"program", "reboot", "parse"),
		"$randomIngverb": oneOf("hacker ing-verbs",
			"backing up", "bypassing", "hacking", "overriding", "compressing", "copying",
			"navigating", "indexing", "connecting", "generating", "quantifying", "calculating",
			"synthesizing", "transmitting", "programming", "parsing"),
		"$randomAdjective": oneOf("hacker adjectives",
			"auxiliary", "primary", "back-end", "digital", "open-source", "virtual", "cross-platform",
			"redundant", "online", "haptic", "multi-byte", "bluetooth", "wireless", "1080p", "neural",
			"optical", "solid state", "mobile"),
		"$randomWord":   match(`^[A-Za-z0-9][A-Za-z0-9/.'-]*$`),
		"$randomWords":  checkRandomWords,
		"$randomPhrase": checkPhrase,

		"$randomLoremWord":       match(`^[a-z]+$`),
		"$randomLoremWords":      match(`^[a-z]+(?: [a-z]+){2}$`),
		"$randomLoremSentence":   checkLoremSentence,
		"$randomLoremSentences":  checkLoremSentences,
		"$randomLoremParagraph":  checkLoremParagraph,
		"$randomLoremParagraphs": checkLoremParagraphs,
		"$randomLoremText":       checkLoremText,
		"$randomLoremSlug":       match(`^[a-z]+(?:-[a-z]+){2}$`),
		"$randomLoremLines":      checkLoremLines,
	}
	return checks
}

func checkTimestamp(value string) error {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return err
	}
	if delta := time.Since(time.Unix(seconds, 0)); delta < -10*time.Second || delta > 10*time.Second {
		return fmt.Errorf("%q is not the current UNIX timestamp", value)
	}
	return nil
}

func checkISOTimestamp(value string) error {
	re := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$`)
	if !re.MatchString(value) {
		return fmt.Errorf("%q is not an ISO 8601 UTC timestamp with milliseconds", value)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return err
	}
	if delta := time.Since(parsed); delta < -10*time.Second || delta > 10*time.Second {
		return fmt.Errorf("%q is not the current time", value)
	}
	return nil
}

func checkRandomInt(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if n < 0 || n > 1000 {
		return fmt.Errorf("%d is outside 0..1000", n)
	}
	return nil
}

func checkIPv4(value string) error {
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("%q is not an IPv4 address", value)
	}
	return nil
}

func checkIPv6(value string) error {
	if !regexp.MustCompile(`^(?:[0-9a-f]{4}:){7}[0-9a-f]{4}$`).MatchString(value) {
		return fmt.Errorf("%q is not eight four-digit IPv6 groups", value)
	}
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is6() {
		return fmt.Errorf("%q is not an IPv6 address", value)
	}
	return nil
}

func checkUserAgent(value string) error {
	if !regexp.MustCompile(`^Mozilla/5\.0 \(.+\)(?: .+)?$`).MatchString(value) {
		return fmt.Errorf("%q is not a browser user agent", value)
	}
	for _, token := range []string{"Firefox/", "Chrome/", "Safari/", "Edg/", "MSIE", "Opera/"} {
		if strings.Contains(value, token) {
			return nil
		}
	}
	return fmt.Errorf("%q names no browser", value)
}

func checkFullName(value string) error {
	re := regexp.MustCompile(`^(?:(?:Mr\.|Mrs\.|Ms\.|Miss|Dr\.) )?[A-Z][A-Za-z' -]+ [A-Z][A-Za-z' -]+(?: (?:Jr\.|Sr\.|I|II|III|IV|V|MD|DDS|PhD|DVM))?$`)
	if !re.MatchString(value) {
		return fmt.Errorf("%q is not a first and last name with an optional prefix or suffix", value)
	}
	return nil
}

func checkLatitude(value string) error {
	if !regexp.MustCompile(`^-?[0-9]{1,2}\.[0-9]{4}$`).MatchString(value) {
		return fmt.Errorf("%q does not have four decimal places", value)
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < -90 || n > 90 {
		return fmt.Errorf("%q is outside -90..90", value)
	}
	return nil
}

func checkLongitude(value string) error {
	if !regexp.MustCompile(`^-?[0-9]{1,3}\.[0-9]{4}$`).MatchString(value) {
		return fmt.Errorf("%q does not have four decimal places", value)
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < -180 || n > 180 {
		return fmt.Errorf("%q is outside -180..180", value)
	}
	return nil
}

func checkAvatarImage(value string) error {
	cloudflare := regexp.MustCompile(`^https://cloudflare-ipfs\.com/ipfs/Qmd3W5DuhgHirLHGVixi6V76LhCkZUz6pnFt5AJBiyvHye/avatar/[0-9]{1,4}\.jpg$`)
	github := regexp.MustCompile(`^https://avatars\.githubusercontent\.com/u/[0-9]{1,8}$`)
	if !cloudflare.MatchString(value) && !github.MatchString(value) {
		return fmt.Errorf("%q is not one of Postman's avatar URLs", value)
	}
	return nil
}

func checkImageDataURI(value string) error {
	const prefix = "data:image/svg+xml;charset=UTF-8,"
	encoded, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return fmt.Errorf("%q does not start with %q", value, prefix)
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(decoded, `<svg `) || !strings.HasSuffix(decoded, `</svg>`) {
		return fmt.Errorf("%q does not decode to an SVG document", value)
	}
	return nil
}

func checkIBAN(value string) error {
	if len(value) < 15 || len(value) > 31 {
		return fmt.Errorf("%q has length %d, want 15..31", value, len(value))
	}
	if !regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]+$`).MatchString(value) {
		return fmt.Errorf("%q is not an uppercase IBAN", value)
	}
	rearranged := value[4:] + value[:4]
	mod := 0
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		if c >= 'A' && c <= 'Z' {
			mod = (mod*100 + int(c-'A') + 10) % 97
		} else {
			mod = (mod*10 + int(c-'0')) % 97
		}
	}
	if mod != 1 {
		return fmt.Errorf("%q fails the IBAN mod-97 check", value)
	}
	return nil
}

func checkCurrencyCode(value string) error {
	codes := []string{
		"AED", "AFN", "ALL", "ARS", "AUD", "BGN", "BOB", "BRL", "CAD", "CDF", "CHF", "CNY",
		"CZK", "DKK", "EUR", "GBP", "GNF", "HKD", "HUF", "INR", "JPY", "KRW", "MXN", "NIO",
		"NOK", "NZD", "PLN", "RUB", "SEK", "SGD", "USD", "XPF", "ZAR", "ZMK",
	}
	if !slices.Contains(codes, value) {
		return fmt.Errorf("%q is not a currency code in the sample table", value)
	}
	return nil
}

func checkCurrencySymbol(value string) error {
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%q is not a non-empty currency symbol", value)
	}
	return nil
}

func checkCompanyName(value string) error {
	re := regexp.MustCompile(`^[A-Z][A-Za-z' -]+(?: (?:Inc|and Sons|LLC|Group)| - [A-Z][A-Za-z' -]+|, [A-Z][A-Za-z' -]+ and [A-Z][A-Za-z' -]+)$`)
	if !re.MatchString(value) {
		return fmt.Errorf("%q is not one of faker's company name shapes", value)
	}
	return nil
}

func checkJSDate(value, relation string) error {
	re := regexp.MustCompile(`^([A-Z][a-z]{2}) ([A-Z][a-z]{2}) ([0-9]{2}) ([0-9]{4}) ([0-9]{2}):([0-9]{2}):([0-9]{2}) GMT([+-])([0-9]{2})([0-9]{2}) \(([^)]+)\)$`)
	parts := re.FindStringSubmatch(value)
	if parts == nil {
		return fmt.Errorf("%q is not a JavaScript Date.toString() value", value)
	}
	months := map[string]time.Month{
		"Jan": time.January, "Feb": time.February, "Mar": time.March, "Apr": time.April,
		"May": time.May, "Jun": time.June, "Jul": time.July, "Aug": time.August,
		"Sep": time.September, "Oct": time.October, "Nov": time.November, "Dec": time.December,
	}
	month, ok := months[parts[2]]
	if !ok {
		return fmt.Errorf("%q has an unknown month", value)
	}
	day, _ := strconv.Atoi(parts[3])
	year, _ := strconv.Atoi(parts[4])
	hour, _ := strconv.Atoi(parts[5])
	minute, _ := strconv.Atoi(parts[6])
	second, _ := strconv.Atoi(parts[7])
	offsetHour, _ := strconv.Atoi(parts[9])
	offsetMinute, _ := strconv.Atoi(parts[10])
	offset := (offsetHour*60 + offsetMinute) * 60
	if parts[8] == "-" {
		offset = -offset
	}
	parsed := time.Date(year, month, day, hour, minute, second, 0, time.FixedZone(parts[11], offset))
	if parsed.Format("Mon") != parts[1] {
		return fmt.Errorf("%q has the wrong weekday", value)
	}
	now := time.Now()
	switch relation {
	case "future":
		if !parsed.After(now.Add(-time.Minute)) {
			return fmt.Errorf("%q is not in the future", value)
		}
	case "past":
		if !parsed.Before(now.Add(time.Minute)) {
			return fmt.Errorf("%q is not in the past", value)
		}
	case "recent":
		if parsed.Before(now.Add(-25*time.Hour)) || parsed.After(now.Add(time.Minute)) {
			return fmt.Errorf("%q is not within the last day", value)
		}
	}
	return nil
}

func checkEmail(value string) error {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return fmt.Errorf("%q is not an email address", value)
	}
	local, domain, ok := strings.Cut(value, "@")
	if !ok || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`).MatchString(local) {
		return fmt.Errorf("%q has an unexpected local part", value)
	}
	if !slices.Contains([]string{"gmail.com", "yahoo.com", "hotmail.com"}, domain) {
		return fmt.Errorf("%q does not use a faker free-email provider", value)
	}
	return nil
}

func checkExampleEmail(value string) error {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return fmt.Errorf("%q is not an email address", value)
	}
	local, domain, ok := strings.Cut(value, "@")
	if !ok || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`).MatchString(local) {
		return fmt.Errorf("%q has an unexpected local part", value)
	}
	if !slices.Contains([]string{"example.com", "example.net", "example.org"}, domain) {
		return fmt.Errorf("%q does not use an example domain", value)
	}
	return nil
}

func checkURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%q is not an HTTP(S) URL", value)
	}
	return nil
}

func checkFilePath(value string) error {
	if !regexp.MustCompile(`^/[A-Za-z0-9_./+-]+$`).MatchString(value) {
		return fmt.Errorf("%q is not an absolute POSIX path", value)
	}
	base := value[strings.LastIndexByte(value, '/')+1:]
	if !strings.Contains(base, ".") {
		return fmt.Errorf("%q has no file extension", value)
	}
	return nil
}

func checkDirectoryPath(value string) error {
	if !regexp.MustCompile(`^/[A-Za-z0-9_./+-]*[A-Za-z0-9_+-]$`).MatchString(value) {
		return fmt.Errorf("%q is not an absolute directory path", value)
	}
	return nil
}

func checkPrice(value string) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`).MatchString(value) {
		return fmt.Errorf("%q does not have two decimal places", value)
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < 0 || n > 1000 {
		return fmt.Errorf("%q is outside 0.00..1000.00", value)
	}
	return nil
}

func checkRandomWords(value string) error {
	parts := strings.Fields(value)
	if len(parts) < 2 || len(parts) > 5 {
		return fmt.Errorf("%q has %d words, want 2..5", value, len(parts))
	}
	for _, part := range parts {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/.'-]*$`).MatchString(part) {
			return fmt.Errorf("%q has an unexpected word %q", value, part)
		}
	}
	return nil
}

func checkPhrase(value string) error {
	if len(value) < 20 || !strings.HasSuffix(value, "!") {
		return fmt.Errorf("%q is not a hacker phrase", value)
	}
	first := value[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return fmt.Errorf("%q does not start with a letter", value)
	}
	if len(strings.Fields(value)) < 5 {
		return fmt.Errorf("%q is too short to be a phrase", value)
	}
	return nil
}

func checkLoremSentence(value string) error {
	if !strings.HasSuffix(value, ".") {
		return fmt.Errorf("%q does not end with a period", value)
	}
	words := strings.Fields(strings.TrimSuffix(value, "."))
	if len(words) < 3 || len(words) > 10 {
		return fmt.Errorf("%q has %d words, want 3..10", value, len(words))
	}
	if first := words[0][0]; first < 'A' || first > 'Z' {
		return fmt.Errorf("%q does not start with a capital letter", value)
	}
	for _, word := range words {
		if !regexp.MustCompile(`^[A-Za-z]+$`).MatchString(word) {
			return fmt.Errorf("%q has an unexpected word %q", value, word)
		}
	}
	return nil
}

func checkLoremSentences(value string) error {
	count := strings.Count(value, ".")
	if count < 2 || count > 6 || !strings.HasSuffix(value, ".") {
		return fmt.Errorf("%q has %d sentences, want 2..6", value, count)
	}
	for sentence := range strings.SplitSeq(value, ". ") {
		if err := checkLoremSentence(strings.TrimSuffix(sentence, ".") + "."); err != nil {
			return err
		}
	}
	return nil
}

func checkLoremParagraph(value string) error {
	count := strings.Count(value, ".")
	if count < 3 || count > 6 || !strings.HasSuffix(value, ".") {
		return fmt.Errorf("%q has %d sentences, want 3..6", value, count)
	}
	return checkLoremSentences(value)
}

func checkLoremParagraphs(value string) error {
	paragraphs := strings.Split(value, "\n \r")
	if len(paragraphs) != 3 {
		return fmt.Errorf("%q has %d paragraphs, want 3 separated by a newline, space, carriage return", value, len(paragraphs))
	}
	for _, paragraph := range paragraphs {
		if err := checkLoremParagraph(paragraph); err != nil {
			return err
		}
	}
	return nil
}

func checkLoremText(value string) error {
	switch {
	case strings.Contains(value, "\n \r"):
		return checkLoremParagraphs(value)
	case strings.Contains(value, "\n"):
		return checkLoremLines(value)
	case strings.Count(value, ".") >= 2:
		return checkLoremSentences(value)
	case strings.HasSuffix(value, "."):
		return checkLoremSentence(value)
	default:
		words := strings.Fields(value)
		if len(words) < 1 || len(words) > 3 {
			return fmt.Errorf("%q has %d words, want 1..3", value, len(words))
		}
		for _, word := range words {
			if !regexp.MustCompile(`^[a-z]+$`).MatchString(word) {
				return fmt.Errorf("%q has an unexpected word %q", value, word)
			}
		}
		return nil
	}
}

func checkLoremLines(value string) error {
	lines := strings.Split(value, "\n")
	if len(lines) < 1 || len(lines) > 5 {
		return fmt.Errorf("%q has %d lines, want 1..5", value, len(lines))
	}
	for _, line := range lines {
		if err := checkLoremSentence(line); err != nil {
			return err
		}
	}
	return nil
}
