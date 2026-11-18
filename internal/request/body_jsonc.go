package request

import (
	"mime"
	"strings"
)

// IsJSONBody reports whether a Raw body is meant to reach the server as JSON.
// Only a JSON media type triggers JSONC stripping: a body with another media
// type may legitimately contain "//" or a trailing comma as data.
func IsJSONBody(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || mediaType == "text/json" || strings.HasSuffix(mediaType, "+json")
}
