package request

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// dynamic are the {{$name}} variables a send generates afresh at each
// occurrence, with Postman's names, so imported collections keep working.
//
// The names are the complete list from Postman's "Use dynamic variables"
// page, https://learning.postman.com/docs/tests-and-scripts/write-scripts/variables-list/
// (checked 2026-11-08). The output shapes follow Postman's implementation in
// postman-collection/lib/superstring/dynamic-variables.js and the
// @faker-js/faker 5.5.3 release its package.json pins, not a newer faker.
//
// This map is the only name list. DynamicNames, DynamicValue, send expansion,
// pm.variables.replaceIn and the editor's completion and hover all read it.
// Import, export and code generation keep the authored {{$name}} text and do
// not enumerate names.
var dynamic = map[string]func() string{
	// Common
	"$guid":         uuid.NewString,
	"$timestamp":    timestamp,
	"$isoTimestamp": isoTimestamp,
	"$randomUUID":   uuid.NewString,

	// Text, numbers, and colors
	"$randomAlphaNumeric": randomAlphaNumeric,
	"$randomBoolean":      randomBoolean,
	"$randomInt":          randomInt,
	"$randomColor":        randomColor,
	"$randomHexColor":     randomHexColor,
	"$randomAbbreviation": randomAbbreviation,

	// Internet and IP addresses
	"$randomIP":         randomIP,
	"$randomIPV6":       randomIPV6,
	"$randomMACAddress": randomMACAddress,
	"$randomPassword":   randomPassword,
	"$randomLocale":     randomLocale,
	"$randomUserAgent":  randomUserAgent,
	"$randomProtocol":   randomProtocol,
	"$randomSemver":     randomSemver,

	// Names
	"$randomFirstName":  randomFirstName,
	"$randomLastName":   randomLastName,
	"$randomFullName":   randomFullName,
	"$randomNamePrefix": randomNamePrefix,
	"$randomNameSuffix": randomNameSuffix,

	// Profession
	"$randomJobArea":       randomJobArea,
	"$randomJobDescriptor": randomJobDescriptor,
	"$randomJobTitle":      randomJobTitle,
	"$randomJobType":       randomJobType,

	// Phone, address, and location
	"$randomPhoneNumber":    randomPhoneNumber,
	"$randomPhoneNumberExt": randomPhoneNumberExt,
	"$randomCity":           randomCity,
	"$randomStreetName":     randomStreetName,
	"$randomStreetAddress":  randomStreetAddress,
	"$randomCountry":        randomCountry,
	"$randomCountryCode":    randomCountryCode,
	"$randomLatitude":       randomLatitude,
	"$randomLongitude":      randomLongitude,

	// Images
	"$randomAvatarImage":    randomAvatarImage,
	"$randomImageUrl":       randomImageURL,
	"$randomAbstractImage":  func() string { return randomCategoryImage("abstract") },
	"$randomAnimalsImage":   func() string { return randomCategoryImage("animals") },
	"$randomBusinessImage":  func() string { return randomCategoryImage("business") },
	"$randomCatsImage":      func() string { return randomCategoryImage("cats") },
	"$randomCityImage":      func() string { return randomCategoryImage("city") },
	"$randomFoodImage":      func() string { return randomCategoryImage("food") },
	"$randomNightlifeImage": func() string { return randomCategoryImage("nightlife") },
	"$randomFashionImage":   func() string { return randomCategoryImage("fashion") },
	"$randomPeopleImage":    func() string { return randomCategoryImage("people") },
	"$randomNatureImage":    func() string { return randomCategoryImage("nature") },
	"$randomSportsImage":    func() string { return randomCategoryImage("sports") },
	"$randomTransportImage": func() string { return randomCategoryImage("transport") },
	"$randomImageDataUri":   randomImageDataURI,

	// Finance
	"$randomBankAccount":     randomBankAccount,
	"$randomBankAccountName": randomBankAccountName,
	"$randomCreditCardMask":  randomCreditCardMask,
	"$randomBankAccountBic":  randomBankAccountBIC,
	"$randomBankAccountIban": randomBankAccountIBAN,
	"$randomTransactionType": randomTransactionType,
	"$randomCurrencyCode":    randomCurrencyCode,
	"$randomCurrencyName":    randomCurrencyName,
	"$randomCurrencySymbol":  randomCurrencySymbol,
	"$randomBitcoin":         randomBitcoin,

	// Business
	"$randomCompanyName":   randomCompanyName,
	"$randomCompanySuffix": randomCompanySuffix,
	"$randomBs":            randomBS,
	"$randomBsAdjective":   randomBSAdjective,
	"$randomBsBuzz":        randomBSBuzz,
	"$randomBsNoun":        randomBSNoun,

	// Catchphrases
	"$randomCatchPhrase":           randomCatchPhrase,
	"$randomCatchPhraseAdjective":  randomCatchPhraseAdjective,
	"$randomCatchPhraseDescriptor": randomCatchPhraseDescriptor,
	"$randomCatchPhraseNoun":       randomCatchPhraseNoun,

	// Databases
	"$randomDatabaseColumn":    randomDatabaseColumn,
	"$randomDatabaseType":      randomDatabaseType,
	"$randomDatabaseCollation": randomDatabaseCollation,
	"$randomDatabaseEngine":    randomDatabaseEngine,

	// Dates
	"$randomDateFuture": randomDateFuture,
	"$randomDatePast":   randomDatePast,
	"$randomDateRecent": randomDateRecent,
	"$randomWeekday":    randomWeekday,
	"$randomMonth":      randomMonth,

	// Domains, emails, and usernames
	"$randomDomainName":   randomDomainName,
	"$randomDomainSuffix": randomDomainSuffix,
	"$randomDomainWord":   randomDomainWord,
	"$randomEmail":        randomEmail,
	"$randomExampleEmail": randomExampleEmail,
	"$randomUserName":     randomUserName,
	"$randomUrl":          randomURL,

	// Files and directories
	"$randomFileName":       randomFileName,
	"$randomFileType":       randomFileType,
	"$randomFileExt":        randomFileExt,
	"$randomCommonFileName": randomCommonFileName,
	"$randomCommonFileType": randomCommonFileType,
	"$randomCommonFileExt":  randomCommonFileExt,
	"$randomFilePath":       randomFilePath,
	"$randomDirectoryPath":  randomDirectoryPath,
	"$randomMimeType":       randomMimeType,

	// Stores
	"$randomPrice":            randomPrice,
	"$randomProduct":          randomProduct,
	"$randomProductAdjective": randomProductAdjective,
	"$randomProductMaterial":  randomProductMaterial,
	"$randomProductName":      randomProductName,
	"$randomDepartment":       randomDepartment,

	// Grammar
	"$randomNoun":      randomNoun,
	"$randomVerb":      randomVerb,
	"$randomIngverb":   randomIngverb,
	"$randomAdjective": randomAdjective,
	"$randomWord":      randomWord,
	"$randomWords":     randomWords,
	"$randomPhrase":    randomPhrase,

	// Lorem ipsum
	"$randomLoremWord":       randomLoremWord,
	"$randomLoremWords":      randomLoremWords,
	"$randomLoremSentence":   randomLoremSentence,
	"$randomLoremSentences":  randomLoremSentences,
	"$randomLoremParagraph":  randomLoremParagraph,
	"$randomLoremParagraphs": randomLoremParagraphs,
	"$randomLoremText":       randomLoremText,
	"$randomLoremSlug":       randomLoremSlug,
	"$randomLoremLines":      randomLoremLines,
}

func timestamp() string { return strconv.FormatInt(time.Now().Unix(), 10) }

func isoTimestamp() string {
	// JavaScript's toISOString always writes three fractional digits.
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
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
