package inspect

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strconv"
	"strings"
)

// ValidatePointer validates a bounded RFC 6901 JSON pointer, not JSONPath.
func ValidatePointer(pointer string) error {
	if len(pointer) > 4096 || pointer != "" && !strings.HasPrefix(pointer, "/") || strings.Count(pointer, "/") > 64 {
		return errors.New("use a JSON pointer such as /data/user/id (at most 64 segments and 4096 bytes)")
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i == len(pointer) || pointer[i] != '0' && pointer[i] != '1' {
				return errors.New("JSON pointer escapes are ~0 for ~ and ~1 for /")
			}
		}
	}
	return nil
}

// JSONField returns a scalar's text without rounding JSON integers. It shares
// the query inspector's strict input/node/depth limits; no partial documents
// or duplicate object names are accepted. Missing fields return empty text.
// Containers and values over 4096 bytes are not suitable for table cells.
func JSONField(ctx context.Context, input, pointer string) (string, error) {
	if err := ValidatePointer(pointer); err != nil {
		return "", err
	}
	if len(input) > queryInputLimit {
		return "", queryLimit("input")
	}
	v, err := decodeQueryInput(&queryBudget{ctx: ctx}, input)
	if err != nil {
		return "", err
	}
	if pointer != "" {
		for raw := range strings.SplitSeq(pointer[1:], "/") {
			key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
			switch value := v.(type) {
			case map[string]any:
				var ok bool
				v, ok = value[key]
				if !ok {
					return "", nil
				}
			case []any:
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(value) || strconv.Itoa(i) != key {
					return "", nil
				}
				v = value[i]
			default:
				return "", nil
			}
		}
	}
	var text string
	switch value := v.(type) {
	case map[string]any, []any:
		return "", errors.New("choose a scalar JSON field, not an object or array")
	case string:
		text = value
	default:
		data, err := json.Marshal(value)
		if err != nil || !jsontext.Value(data).IsValid() {
			return "", errors.New("cannot represent JSON field")
		}
		text = string(data)
	}
	if len(text) > 4096 {
		return "", errors.New("JSON column value exceeds 4096 bytes")
	}
	return text, ctx.Err()
}
