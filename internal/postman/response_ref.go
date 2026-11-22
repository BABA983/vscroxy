package postman

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/request"
)

// responseRefMarker is written into generated Postman pre-request scripts so
// an import can report that the script is still a script, not a native
// response reference.
const responseRefMarker = "vscroxy response reference:"

type exportedResponseRef struct {
	name     string
	target   request.ResponseRef
	variable string
}

// convertResponseRefs turns each {{response "Login" "/token"}} into a
// Postman collection variable filled by a pre-request script. The source is
// settled to a stable ID at export time, so a later rename in vscroxy does
// not change the script. A reference whose source is missing or outside the
// collection is left as-is and counted for the export report.
func convertResponseRefs(requests []request.Request) ([]request.Request, int) {
	var unresolved int
	out := make([]request.Request, len(requests))
	for i, r := range requests {
		byKey := map[string]exportedResponseRef{}
		var scripts []string
		converted := r
		rewrite := func(s string) string {
			return request.ReplaceResponseRefs(s, func(ref request.ResponseRef) string {
				key := request.ResponseRefKey(ref)
				if existing, ok := byKey[key]; ok {
					return "{{" + existing.variable + "}}"
				}
				source, err := library.FindRequest(requests, ref.Name)
				root, _, _ := strings.Cut(strings.Trim(r.Collection, "/"), "/")
				sourceRoot, _, _ := strings.Cut(strings.Trim(source.Collection, "/"), "/")
				_, pointerErr := pointerSegments(ref.Pointer)
				_, isHeader := strings.CutPrefix(ref.Pointer, "header:")
				if err != nil || root != sourceRoot || source.ID == "" || !isHeader && pointerErr != nil {
					unresolved++
					return s[ref.Start:ref.End] // Leave it visible in the export.
				}
				variable := responseRefVariable(source.ID, ref.Pointer)
				byKey[key] = exportedResponseRef{name: source.Name, target: ref, variable: variable}
				scripts = append(scripts, responseRefScript(source.ID, ref, variable))
				return "{{" + variable + "}}"
			})
		}
		converted = converted.RewriteResponseRefs(rewrite)
		if len(scripts) > 0 {
			converted.Pre = strings.Join(scripts, "\n;\n") + "\n;\n" + converted.Pre
		}
		out[i] = converted
	}
	return out, unresolved
}

func responseRefVariable(id, pointer string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + pointer))
	return "__vscroxy_rr_" + hex.EncodeToString(sum[:6])
}

// responseRefScript needs no captured value: it asks Postman to run the
// source request, then walks the pointer or reads the named response header.
func responseRefScript(id string, ref request.ResponseRef, variable string) string {
	source, _ := json.Marshal(id)
	key, _ := json.Marshal(variable)
	label := "// " + responseRefMarker + " " + ref.Name + " " + ref.Pointer + "\n"
	if header, ok := strings.CutPrefix(ref.Pointer, "header:"); ok {
		header, _ := json.Marshal(strings.TrimSpace(header))
		return label + fmt.Sprintf(`(async function () {
  if (pm.collectionVariables.has(%s)) return;
  var res = await pm.execution.runRequest(%s);
  if (!res || res.code < 200 || res.code >= 300) throw new Error("Response reference source did not succeed");
  var value = res.headers && res.headers.get && res.headers.get(%s) || "";
  if (typeof value !== "string" || value === "") throw new Error("Response reference header is missing");
  pm.collectionVariables.set(%s, value);
})();`, key, source, header, key)
	}
	segments, err := pointerSegments(ref.Pointer)
	if err != nil {
		return ""
	}
	fields, _ := json.Marshal(segments)
	return label + fmt.Sprintf(`(async function () {
  if (pm.collectionVariables.has(%s)) return;
  var res = await pm.execution.runRequest(%s);
  if (!res || res.code < 200 || res.code >= 300) throw new Error("Response reference source did not succeed");
  var value;
  try { value = res.json(); } catch (_) { throw new Error("Response reference source is not valid JSON"); }
  %s.forEach(function (part) {
    if (value === null || typeof value !== "object" || !Object.prototype.hasOwnProperty.call(value, part)) {
      throw new Error("Response reference pointer is missing");
    }
    value = value[part];
  });
  if (typeof value !== "string") throw new Error("Response reference value is not a string");
  pm.collectionVariables.set(%s, value);
})();`, key, source, fields, key)
}

// pointerSegments is the RFC 6901 decoding the generated script needs. It is
// kept here so an invalid pointer is reported as an unconverted reference
// rather than producing a script that walks the wrong field.
func pointerSegments(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("not a JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	out := make([]string, len(raw))
	for i, part := range raw {
		var b strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				b.WriteByte(part[j])
				continue
			}
			j++
			if j >= len(part) {
				return nil, fmt.Errorf("invalid pointer escape")
			}
			switch part[j] {
			case '0':
				b.WriteByte('~')
			case '1':
				b.WriteByte('/')
			default:
				return nil, fmt.Errorf("invalid pointer escape")
			}
		}
		out[i] = b.String()
	}
	return out, nil
}

// hasImportedResponseRef reports the marker of a converted script, so the
// import report can say the response reference is still a script.
func hasImportedResponseRef(scripts ...string) bool {
	return slices.ContainsFunc(scripts, func(s string) bool { return strings.Contains(s, responseRefMarker) })
}
