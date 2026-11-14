package app

// Options whose stored value is an identifier the app compares, and what a
// screen calls them. The value stays as it is — "Raw", "In", "Text" — and
// only the trigger, the popup or the chip shows the translation. Values that
// name a protocol or format (HTTP, HTTPS, WebSocket, SSE, JSON, GraphQL, Hex,
// Base64, HTML) are shown as they are.

import (
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/i18n"
)

// The fixed option sets the screens show. Keeping them here lets the tests
// walk every value: each must have a message or be a known identifier.
var (
	bodyTypes       = []string{"Raw", "URL encoded", "Multipart", "Binary", "GraphQL"}
	responseFormats = []string{"Auto", "Raw", "Hex", "Pretty"}
	directions      = []string{"All", "In", "Out", "Events"}
)

// optionSets are the fixed sets a screen shows, for the test that every
// value is translated or a known identifier.
var optionSets = map[string][]string{
	"capture kinds":     captureKinds,
	"capture protocols": captureProtocols,
	"body types":        bodyTypes,
	"response formats":  responseFormats,
	"directions":        directions,
}

// optionLabel says an option's value in the app's language. A value without a
// message is an identifier and stays as it is.
func optionLabel(value string) string {
	return optionLabelFor(locales.In(i18n.Locale()), value)
}

// optionLabelFor says an option in the language of msgs, for the test that
// every value has a message.
func optionLabelFor(msgs locales.Messages, value string) string {
	switch value {
	case "All":
		return msgs.All()
	case "Text":
		return msgs.SearchText()
	case "Wildcard":
		return msgs.Wildcard()
	case "Regex":
		return msgs.Regex()
	case "Raw":
		return msgs.Raw()
	case "URL encoded":
		return msgs.UrlEncoded()
	case "Multipart":
		return msgs.Multipart()
	case "Binary":
		return msgs.Binary()
	case "Auto":
		return msgs.Auto()
	case "Pretty":
		return msgs.Pretty()
	case "Preview":
		return msgs.Preview()
	case "Form":
		return msgs.Form()
	case "Media":
		return msgs.Media()
	case "In":
		return msgs.InText()
	case "Out":
		return msgs.Out()
	case "Events":
		return msgs.Events()
	case "Images":
		return msgs.Images()
	case "Response body":
		return msgs.ResponseBody()
	case "Response headers":
		return msgs.ResponseHeaders()
	case "Request body":
		return msgs.RequestBody()
	case "Request headers":
		return msgs.RequestHeaders()
	case "All (AND)":
		return msgs.AllAnd()
	case "Any (OR)":
		return msgs.AnyOr()
	case "None":
		return msgs.None()
	case "Inherit":
		return msgs.Inherit()
	case "Automatic":
		return msgs.Automatic()
	case "URL component":
		return msgs.UrlComponent()
	case "URL query":
		return msgs.UrlQuery()
	case "Unix seconds":
		return msgs.UnixSeconds()
	case "Unix milliseconds":
		return msgs.UnixMilliseconds()
	case "Basic header":
		return msgs.BasicHeader()
	case "In body":
		return msgs.InBody()
	case "New environment":
		return msgs.NewEnvironment()
	case "All statuses":
		return msgs.AllStatuses()
	case "Capture-linked":
		return msgs.CaptureLinked()
	case "Authored":
		return msgs.Authored()
	case "All sources":
		return msgs.AllSources()
	case "Any":
		return msgs.Any()
	case "Marked":
		return msgs.Marked()
	case "Red":
		return msgs.ColorRed()
	case "Orange":
		return msgs.ColorOrange()
	case "Yellow":
		return msgs.ColorYellow()
	case "Green":
		return msgs.ColorGreen()
	case "Blue":
		return msgs.ColorBlue()
	case "Purple":
		return msgs.ColorPurple()
	case "No response":
		return msgs.NoResponse()
	case "equals":
		return msgs.Equals()
	case "matches":
		return msgs.Matches()
	case "present":
		return msgs.Present()
	case "Header":
		return msgs.HeaderLocation()
	case "Query":
		return msgs.QueryLocation()
	case "Body":
		return msgs.BodyLocation()
	case "Request header":
		return msgs.RequestHeader()
	case "Response header":
		return msgs.ResponseHeader()
	case "Query parameter":
		return msgs.QueryParameter()
	case "Request JSON":
		return msgs.RequestJson()
	case "Response JSON":
		return msgs.ResponseJson()
	case "GraphQL operation":
		return msgs.GraphqlOperation()
	case "Environment current value":
		return msgs.EnvironmentCurrentValue()
	case "Vault":
		return msgs.Vault2()
	case "Leave in requests":
		return msgs.LeaveInRequests()
	case "text":
		return msgs.Text()
	case "json":
		return "JSON"
	case "hex":
		return msgs.Hex()
	case "base64":
		return "Base64"
	}
	return value
}

// optionIdentifiers are option values that name a protocol, a format, a
// language or a status and stay as they are in every language. The test uses
// the list to tell a deliberately kept identifier from a missed translation.
var optionIdentifiers = map[string]bool{
	"2xx": true, "3xx": true, "4xx": true, "5xx": true,
	"HTTP": true, "HTTPS": true, "WebSocket": true, "WS": true, "SSE": true,
	"JSON": true, "GraphQL": true, "Hex": true, "Base64": true, "HTML": true, "Multipart": true,
	"Protobuf": true, "JWT": true, "JSONPath": true, "jq": true,
	"MQTT 3.1.1": true, "MQTT 5.0": true, "auto": true,
	"cURL": true, "Node.js": true, "Python": true, "Go": true,
	"JavaScript": true, "TypeScript": true, "Java": true, "C#": true,
	"PHP": true, "Ruby": true, "Swift": true, "Kotlin": true,
	"Basic": true, "Bearer": true, "Digest": true, "OAuth 1.0": true,
	"OAuth 2.0": true, "Hawk": true, "AWS Signature": true, "NTLM": true,
	"Cookie": true,
	"XML":    true, "JS": true, "CSS": true, "h1": true, "h2": true,
	"Streamable HTTP": true, "stdio": true,
	"502 Bad Gateway": true, "429 Too Many Requests": true, "503 Service Unavailable": true,
}
