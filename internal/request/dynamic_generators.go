package request

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

func pick(values []string) string { return values[rand.IntN(len(values))] }

func randomText(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return string(b)
}

func randomDigits(n int) string { return randomText(n, digits) }

func randomAlphaNumeric() string { return randomText(1, alphanumeric) }

func randomBoolean() string { return strconv.FormatBool(rand.IntN(2) == 1) }

func randomInt() string { return strconv.Itoa(rand.IntN(1001)) }

func randomColor() string { return pick(colors) }

func randomHexColor() string { return fmt.Sprintf("#%06x", rand.IntN(1<<24)) }

func randomAbbreviation() string { return pick(hackerAbbreviations) }

func randomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256))
}

func randomIPV6() string {
	var b strings.Builder
	for i := range 8 {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%04x", rand.IntN(1<<16))
	}
	return b.String()
}

func randomMACAddress() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256), rand.IntN(256))
}

func randomPassword() string { return randomText(15, alphanumeric) }

func randomLocale() string { return pick(locales) }

func randomUserAgent() string {
	firefox := 100 + rand.IntN(30)
	chrome := 100 + rand.IntN(30)
	switch rand.IntN(8) {
	case 0:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%d.0) Gecko/20100101 Firefox/%d.0", firefox, firefox)
	case 1:
		return fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", chrome)
	case 2:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", chrome)
	case 3:
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15"
	case 4:
		return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64; rv:%d.0) Gecko/20100101 Firefox/%d.0", firefox, firefox)
	case 5:
		return "Mozilla/5.0 (compatible; MSIE 10.0; Windows NT 6.2; Trident/6.0)"
	case 6:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36 Edg/%d.0.0.0", chrome, chrome)
	default:
		return fmt.Sprintf("Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Mobile Safari/537.36", chrome)
	}
}

func randomProtocol() string { return pick(protocols) }

func randomSemver() string {
	return fmt.Sprintf("%d.%d.%d", rand.IntN(10), rand.IntN(10), rand.IntN(10))
}

func randomFirstName() string { return pick(firstNames) }

func randomLastName() string { return pick(lastNames) }

func randomFullName() string {
	first, last := pick(firstNames), pick(lastNames)
	switch rand.IntN(9) {
	case 0:
		return pick(namePrefixes) + " " + first + " " + last
	case 1:
		return first + " " + last + " " + pick(nameSuffixes)
	default:
		return first + " " + last
	}
}

func randomNamePrefix() string { return pick(namePrefixes) }

func randomNameSuffix() string { return pick(nameSuffixes) }

func randomJobArea() string { return pick(jobAreas) }

func randomJobDescriptor() string { return pick(jobDescriptors) }

func randomJobTitle() string {
	return randomJobDescriptor() + " " + randomJobArea() + " " + randomJobType()
}

func randomJobType() string { return pick(jobTypes) }

func randomPhoneNumber() string {
	return randomDigits(3) + "-" + randomDigits(3) + "-" + randomDigits(4)
}

func randomPhoneNumberExt() string {
	return strconv.Itoa(1+rand.IntN(99)) + "-" + randomPhoneNumber()
}

func randomCity() string {
	switch rand.IntN(5) {
	case 0:
		return pick(cityPrefixes) + " " + pick(firstNames) + pick(citySuffixes)
	case 1:
		return pick(cityPrefixes) + " " + pick(firstNames)
	case 2:
		return pick(firstNames) + pick(citySuffixes)
	case 3:
		return pick(lastNames) + pick(citySuffixes)
	default:
		return pick(cityNames)
	}
}

func randomStreetName() string {
	name := pick(firstNames)
	if rand.IntN(2) == 0 {
		name = pick(lastNames)
	}
	return name + " " + pick(streetSuffixes)
}

func randomStreetAddress() string {
	return strconv.Itoa(100+rand.IntN(99_900)) + " " + randomStreetName()
}

func randomCountry() string { return pick(countries) }

func randomCountryCode() string { return pick(countryCodes) }

func randomLatitude() string {
	return fmt.Sprintf("%.4f", float64(rand.IntN(1_800_001)-900_000)/10_000)
}

func randomLongitude() string {
	return fmt.Sprintf("%.4f", float64(rand.IntN(3_600_001)-1_800_000)/10_000)
}

func randomAvatarImage() string {
	if rand.IntN(2) == 0 {
		return fmt.Sprintf("https://cloudflare-ipfs.com/ipfs/Qmd3W5DuhgHirLHGVixi6V76LhCkZUz6pnFt5AJBiyvHye/avatar/%d.jpg", rand.IntN(1250))
	}
	return fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", rand.IntN(100_000_000))
}

// randomImageURL follows the faker 5.5.3 provider Postman pins. The Postman
// docs page still shows lorempixel.com, an older provider.
func randomImageURL() string { return "http://placeimg.com/640/480" }

func randomCategoryImage(category string) string {
	return "http://placeimg.com/640/480/" + category
}

func randomImageDataURI() string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" version="1.1" baseProfile="full" width="640" height="480"><rect width="100%" height="100%" fill="grey"/><text x="320" y="240" font-size="20" alignment-baseline="middle" text-anchor="middle" fill="white">640x480</text></svg>`
	return "data:image/svg+xml;charset=UTF-8," + encodeURIComponent(svg)
}

// encodeURIComponent matches JavaScript's encodeURIComponent for the ASCII
// SVG above, so the data URI has the same escaping as faker's.
func encodeURIComponent(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(unreserved, s[i]) >= 0 {
			b.WriteByte(s[i])
		} else {
			fmt.Fprintf(&b, "%%%02X", s[i])
		}
	}
	return b.String()
}

func randomBankAccount() string { return randomDigits(8) }

func randomBankAccountName() string { return pick(accountTypes) + " Account" }

func randomCreditCardMask() string { return randomDigits(4) }

func randomBankAccountBIC() string {
	vowels := []string{"A", "E", "I", "O", "U"}
	bic := randomText(3, upperAlpha) + pick(vowels) + pick(countryCodes) + randomText(1, upperAlpha) + "1"
	switch prob := rand.IntN(100); {
	case prob < 10:
		return bic + randomText(1, upperAlpha) + pick(vowels) + randomText(1, upperAlpha)
	case prob < 40:
		return bic + randomDigits(3)
	default:
		return bic
	}
}

func randomBankAccountIBAN() string {
	format := ibanFormats[rand.IntN(len(ibanFormats))]
	var b strings.Builder
	for _, part := range format.Parts {
		for range part.Count {
			switch part.Kind {
			case 'a':
				b.WriteByte(upperAlpha[rand.IntN(len(upperAlpha))])
			case 'c':
				if rand.IntN(100) < 80 {
					b.WriteByte(byte('0' + rand.IntN(10)))
				} else {
					b.WriteByte(upperAlpha[rand.IntN(len(upperAlpha))])
				}
			default:
				b.WriteByte(byte('0' + rand.IntN(10)))
			}
		}
	}
	bban := b.String()
	check := 98 - ibanMod97(bban+format.Country+"00")
	return fmt.Sprintf("%s%02d%s", format.Country, check, bban)
}

func ibanMod97(s string) int {
	m := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			m = (m*100 + int(c-'A') + 10) % 97
		} else {
			m = (m*10 + int(c-'0')) % 97
		}
	}
	return m
}

func randomTransactionType() string { return pick(transactionTypes) }

func randomCurrencyCode() string { return currencies[rand.IntN(len(currencies))].Code }

func randomCurrencyName() string { return currencies[rand.IntN(len(currencies))].Name }

func randomCurrencySymbol() string { return currencies[rand.IntN(len(currencies))].Symbol }

func randomBitcoin() string {
	length := 25 + rand.IntN(10)
	var b strings.Builder
	b.WriteByte("13"[rand.IntN(2)])
	for range length - 1 {
		b.WriteByte(base58Alphabet[rand.IntN(len(base58Alphabet))])
	}
	return b.String()
}

func randomCompanyName() string {
	switch rand.IntN(3) {
	case 0:
		return pick(lastNames) + " " + pick(companySuffixes)
	case 1:
		return pick(lastNames) + " - " + pick(lastNames)
	default:
		return pick(lastNames) + ", " + pick(lastNames) + " and " + pick(lastNames)
	}
}

func randomCompanySuffix() string { return pick(companySuffixes) }

func randomBS() string {
	return pick(bsBuzzes) + " " + pick(bsAdjectives) + " " + pick(bsNouns)
}

func randomBSAdjective() string { return pick(bsAdjectives) }

func randomBSBuzz() string { return pick(bsBuzzes) }

func randomBSNoun() string { return pick(bsNouns) }

func randomCatchPhrase() string {
	return pick(catchPhraseAdjectives) + " " + pick(catchPhraseDescriptors) + " " + pick(catchPhraseNouns)
}

func randomCatchPhraseAdjective() string { return pick(catchPhraseAdjectives) }

func randomCatchPhraseDescriptor() string { return pick(catchPhraseDescriptors) }

func randomCatchPhraseNoun() string { return pick(catchPhraseNouns) }

func randomDatabaseColumn() string { return pick(databaseColumns) }

func randomDatabaseType() string { return pick(databaseTypes) }

func randomDatabaseCollation() string { return pick(databaseCollations) }

func randomDatabaseEngine() string { return pick(databaseEngines) }

func randomDateFuture() string {
	return jsDate(time.Now().Add(time.Duration(rand.Int64N(int64(365 * 24 * time.Hour)))))
}

func randomDatePast() string {
	return jsDate(time.Now().Add(-time.Duration(rand.Int64N(int64(365 * 24 * time.Hour)))))
}

func randomDateRecent() string {
	return jsDate(time.Now().Add(-time.Duration(rand.Int64N(int64(24 * time.Hour)))))
}

// jsDate is JavaScript's Date.toString(), which is how Postman renders the
// Date objects returned by faker.date.past, future and recent.
func jsDate(t time.Time) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return t.Format("Mon Jan 02 2006 15:04:05") +
		fmt.Sprintf(" GMT%s%02d%02d (%s)", sign, offset/3600, offset%3600/60, t.Format("MST"))
}

func randomWeekday() string { return pick(weekdays) }

func randomMonth() string { return pick(months) }

func randomDomainName() string { return randomDomainWord() + "." + randomDomainSuffix() }

func randomDomainSuffix() string { return pick(domainSuffixes) }

func randomDomainWord() string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= 'a' && r <= 'z':
			return r
		default:
			return -1
		}
	}, pick(firstNames))
}

func randomEmail() string { return randomUserName() + "@" + pick(freeEmailProviders) }

func randomExampleEmail() string {
	return randomUserName() + "@" + pick(exampleEmailProviders)
}

func randomUserName() string {
	first, last := pick(firstNames), pick(lastNames)
	var name string
	switch rand.IntN(3) {
	case 0:
		name = first + strconv.Itoa(rand.IntN(100))
	case 1:
		name = first + pick([]string{".", "_"}) + last
	default:
		name = first + pick([]string{".", "_"}) + last + strconv.Itoa(rand.IntN(100))
	}
	return strings.NewReplacer("'", "", " ", "").Replace(name)
}

func randomURL() string { return randomProtocol() + "://" + randomDomainName() }

func fileBase(minWords, maxWords int) string {
	base := strings.ToLower(strings.Join(randomWordsRange(minWords, maxWords), "_"))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, base)
}

func randomFileName() string { return fileBase(1, 3) + "." + randomFileExt() }

func randomFileType() string { return pick(fileTypes) }

func randomFileExt() string { return pick(fileExtensions) }

func randomCommonFileName() string { return fileBase(1, 3) + "." + randomCommonFileExt() }

func randomCommonFileType() string { return pick(commonFileTypes) }

func randomCommonFileExt() string { return pick(commonFileExtensions) }

func randomFilePath() string { return randomDirectoryPath() + "/" + randomFileName() }

func randomDirectoryPath() string { return pick(directoryPaths) }

func randomMimeType() string { return pick(mimeTypes) }

func randomPrice() string {
	cents := rand.IntN(100_001)
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func randomProduct() string { return pick(products) }

func randomProductAdjective() string { return pick(productAdjectives) }

func randomProductMaterial() string { return pick(productMaterials) }

func randomProductName() string {
	return randomProductAdjective() + " " + randomProductMaterial() + " " + randomProduct()
}

func randomDepartment() string { return pick(departments) }

func randomNoun() string { return pick(hackerNouns) }

func randomVerb() string { return pick(hackerVerbs) }

func randomIngverb() string { return pick(hackerIngverbs) }

func randomAdjective() string { return pick(hackerAdjectives) }

var randomWordSources = [][]string{
	colors, departments, productAdjectives, productMaterials, products,
	companyAdjectives, companyDescriptors, companyNouns,
	bsAdjectives, bsBuzzes, bsNouns,
	streetSuffixes, countries, accountTypes, transactionTypes,
	hackerNouns, hackerVerbs, hackerAdjectives, hackerIngverbs,
	hackerAbbreviations, jobDescriptors, jobAreas, jobTypes,
}

// randomWord mirrors faker.random.word: pick one of several word-producing
// categories, then one space-separated token from its result.
func randomWord() string {
	source := randomWordSources[rand.IntN(len(randomWordSources))]
	parts := strings.Fields(source[rand.IntN(len(source))])
	return parts[rand.IntN(len(parts))]
}

func randomWordsRange(minWords, maxWords int) []string {
	words := make([]string, minWords+rand.IntN(maxWords-minWords+1))
	for i := range words {
		words[i] = randomWord()
	}
	return words
}

func randomWords() string { return strings.Join(randomWordsRange(2, 5), " ") }

func randomPhrase() string { return expandHackerPhrase(pick(hackerPhrases)) }

func expandHackerPhrase(phrase string) string {
	for {
		start := strings.Index(phrase, "{{")
		if start < 0 {
			return phrase
		}
		end := strings.Index(phrase[start:], "}}")
		if end < 0 {
			return phrase
		}
		end += start
		var value string
		switch phrase[start+2 : end] {
		case "verb":
			value = randomVerb()
		case "noun":
			value = randomNoun()
		case "abbreviation":
			value = randomAbbreviation()
		case "adjective":
			value = randomAdjective()
		case "ingverb":
			value = randomIngverb()
		default:
			value = phrase[start : end+2]
		}
		phrase = phrase[:start] + value + phrase[end+2:]
	}
}

func randomLoremWord() string { return pick(loremWords) }

func loremWordsN(n int) []string {
	words := make([]string, n)
	for i := range words {
		words[i] = randomLoremWord()
	}
	return words
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func randomLoremWords() string { return strings.Join(loremWordsN(3), " ") }

func randomLoremSentence() string {
	words := loremWordsN(3 + rand.IntN(8))
	words[0] = capitalize(words[0])
	return strings.Join(words, " ") + "."
}

func randomLoremSentences() string {
	sentences := make([]string, 2+rand.IntN(5))
	for i := range sentences {
		sentences[i] = randomLoremSentence()
	}
	return strings.Join(sentences, " ")
}

func randomLoremParagraph() string {
	sentences := make([]string, 3+rand.IntN(4))
	for i := range sentences {
		sentences[i] = randomLoremSentence()
	}
	return strings.Join(sentences, " ")
}

func randomLoremParagraphs() string {
	paragraphs := make([]string, 3)
	for i := range paragraphs {
		paragraphs[i] = randomLoremParagraph()
	}
	// faker 5.5.3 joins paragraphs with a newline, space, carriage return.
	return strings.Join(paragraphs, "\n \r")
}

func randomLoremText() string {
	generators := []func() string{
		randomLoremWord,
		randomLoremWords,
		randomLoremSentence,
		randomLoremSentences,
		randomLoremParagraph,
		randomLoremParagraphs,
		randomLoremLines,
	}
	return generators[rand.IntN(len(generators))]()
}

func randomLoremSlug() string { return strings.Join(loremWordsN(3), "-") }

func randomLoremLines() string {
	lines := make([]string, 1+rand.IntN(5))
	for i := range lines {
		lines[i] = randomLoremSentence()
	}
	return strings.Join(lines, "\n")
}
