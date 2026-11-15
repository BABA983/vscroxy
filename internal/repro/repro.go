// Package repro is the one-file reproduction bundle exported from Sessions.
// It holds redacted flows, their notes and timing, and an embedded Postman
// collection that vscroxy-cli run can execute.
package repro

import (
	"encoding/json/v2"
	"fmt"
	"time"
)

// Format and Version identify a bundle.
const (
	Format  = "vscroxy-repro"
	Version = 1
)

// Bundle is the exported file.
type Bundle struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	Title      string    `json:"title,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	Created    time.Time `json:"created,omitzero"`
	Flows      []Flow    `json:"flows"`
	Collection any       `json:"collection"`
}

// Flow is one redacted exchange and what a person marked on it.
type Flow struct {
	ID              int       `json:"id"`
	Method          string    `json:"method"`
	URL             string    `json:"url"`
	RequestHeaders  string    `json:"requestHeaders,omitempty"`
	RequestBody     string    `json:"requestBody,omitempty"`
	RequestBytes    []byte    `json:"requestBytes,omitempty"`
	ResponseStatus  int       `json:"responseStatus,omitempty"`
	ResponseHeaders string    `json:"responseHeaders,omitempty"`
	ResponseBody    string    `json:"responseBody,omitempty"`
	ResponseBytes   []byte    `json:"responseBytes,omitempty"`
	Millis          int       `json:"millis,omitempty"`
	Wait            int       `json:"waitMillis,omitempty"`
	Download        int       `json:"downloadMillis,omitempty"`
	Timing          any       `json:"timing,omitempty"`
	At              time.Time `json:"at,omitzero"`
	Kind            string    `json:"kind,omitempty"`
	Note            string    `json:"note,omitempty"`
	Color           string    `json:"color,omitempty"`
	Pinned          bool      `json:"pinned,omitzero"`
	Strike          bool      `json:"strike,omitzero"`
}

// Parse reads a bundle, rejecting another format or version.
func Parse(data []byte) (*Bundle, error) {
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, err
	}
	if bundle.Format != Format {
		return nil, fmt.Errorf("not a vscroxy reproduction bundle")
	}
	if bundle.Version != Version {
		return nil, fmt.Errorf("unsupported reproduction bundle version %d", bundle.Version)
	}
	if bundle.Collection == nil {
		return nil, fmt.Errorf("the reproduction bundle has no collection")
	}
	return &bundle, nil
}

// Collection returns the embedded Postman collection, if data is a bundle.
func Collection(data []byte) ([]byte, bool) {
	bundle, err := Parse(data)
	if err != nil {
		return nil, false
	}
	collection, err := json.Marshal(bundle.Collection)
	if err != nil {
		return nil, false
	}
	return collection, true
}
