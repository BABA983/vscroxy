// Command gen regenerates the Unicode tables embedded by internal/unicodechars.
//
// The data comes from src/vs/base/common/strings.ts in microsoft/vscode at the
// commit named below. By default the generator reads that file from a local
// checkout (VSCODE_DIR, then /tmp/vscode) or fetches the same pinned revision
// from GitHub. Use -source to read an arbitrary copy of strings.ts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	vscodeCommit = "972b66fdc541e10689adfd90ade9549dea1b18bc"
	stringsPath  = "src/vs/base/common/strings.ts"
	rawURL       = "https://raw.githubusercontent.com/microsoft/vscode/" + vscodeCommit + "/" + stringsPath
)

var jsonLiteral = regexp.MustCompile(`JSON\.parse\(\s*'((?:[^'\\]|\\.)*)'\s*\)`)

type ambiguousPair struct {
	codePoint rune
	primary   rune
}

type runeRange struct {
	first rune
	last  rune
}

type data struct {
	ambiguous map[string][]ambiguousPair
	invisible []runeRange
}

func main() {
	source := flag.String("source", "", "path to a local copy of strings.ts")
	out := flag.String("out", "data_generated.go", "generated Go file to write")
	flag.Parse()

	src, err := readSource(*source)
	if err != nil {
		fail(err)
	}
	parsed, err := parse(src)
	if err != nil {
		fail(err)
	}
	generated, err := render(parsed)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, generated, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}

func readSource(source string) ([]byte, error) {
	if source != "" {
		return os.ReadFile(source)
	}
	if dir := os.Getenv("VSCODE_DIR"); dir != "" {
		if src, ok := readGitSource(dir); ok {
			return src, nil
		}
		return nil, fmt.Errorf("VSCODE_DIR %q does not contain %s at %s", dir, stringsPath, vscodeCommit)
	}
	if src, ok := readGitSource("/tmp/vscode"); ok {
		return src, nil
	}
	return fetchSource()
}

func readGitSource(dir string) ([]byte, bool) {
	out, err := exec.Command("git", "-C", dir, "show", vscodeCommit+":"+stringsPath).Output()
	return out, err == nil
}

func fetchSource() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func parse(src []byte) (data, error) {
	ambiguous, err := parseJSONLiteral(src, "export class AmbiguousCharacters", "private static readonly cache")
	if err != nil {
		return data{}, fmt.Errorf("ambiguous character data: %w", err)
	}
	invisible, err := parseJSONLiteral(src, "export class InvisibleCharacters", "private static _data")
	if err != nil {
		return data{}, fmt.Errorf("invisible character data: %w", err)
	}

	result := data{ambiguous: make(map[string][]ambiguousPair, len(ambiguous))}
	for locale, flat := range ambiguous {
		if len(flat)%2 != 0 {
			return data{}, fmt.Errorf("locale %q has an odd number of code points", locale)
		}
		// JSON.parse into a JS Map keeps the last value for duplicate keys.
		byCodePoint := make(map[rune]rune, len(flat)/2)
		for i := 0; i < len(flat); i += 2 {
			codePoint, primary := rune(flat[i]), rune(flat[i+1])
			if !utf8.ValidRune(codePoint) || !utf8.ValidRune(primary) {
				return data{}, fmt.Errorf("locale %q has an invalid code point pair %d, %d", locale, flat[i], flat[i+1])
			}
			byCodePoint[codePoint] = primary
		}
		keys := make([]rune, 0, len(byCodePoint))
		for codePoint := range byCodePoint {
			keys = append(keys, codePoint)
		}
		slices.Sort(keys)
		pairs := make([]ambiguousPair, 0, len(keys))
		for _, codePoint := range keys {
			pairs = append(pairs, ambiguousPair{codePoint, byCodePoint[codePoint]})
		}
		result.ambiguous[locale] = pairs
	}

	// VS Code flattens every locale's invisible data into one set.
	seen := make(map[rune]bool)
	for _, flat := range invisible {
		for _, value := range flat {
			codePoint := rune(value)
			if !utf8.ValidRune(codePoint) {
				return data{}, fmt.Errorf("invalid invisible code point %d", value)
			}
			seen[codePoint] = true
		}
	}
	codePoints := make([]rune, 0, len(seen))
	for codePoint := range seen {
		codePoints = append(codePoints, codePoint)
	}
	slices.Sort(codePoints)
	for _, codePoint := range codePoints {
		if n := len(result.invisible); n > 0 && result.invisible[n-1].last+1 == codePoint {
			result.invisible[n-1].last = codePoint
			continue
		}
		result.invisible = append(result.invisible, runeRange{codePoint, codePoint})
	}
	return result, nil
}

func parseJSONLiteral(src []byte, classMarker, stopMarker string) (map[string][]int, error) {
	start := bytes.Index(src, []byte(classMarker))
	if start < 0 {
		return nil, fmt.Errorf("missing %q", classMarker)
	}
	stop := bytes.Index(src[start:], []byte(stopMarker))
	if stop < 0 {
		return nil, fmt.Errorf("missing %q after %q", stopMarker, classMarker)
	}
	match := jsonLiteral.FindSubmatch(src[start : start+stop])
	if match == nil {
		return nil, fmt.Errorf("missing JSON.parse string literal")
	}
	// The literal is single-quoted JavaScript with escaped double quotes.
	literal := strings.ReplaceAll(string(match[1]), `\"`, `"`)
	var raw map[string][]int
	if err := json.Unmarshal([]byte(literal), &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	return raw, nil
}

func render(parsed data) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `// Code generated by go generate ./gen from microsoft/vscode@%s; DO NOT EDIT.
// Source: %s, AmbiguousCharacters.ambiguousCharacterData and
// InvisibleCharacters.getRawData (generated upstream by hediet/vscode-unicode-data).
// Regenerate from the repository root with: go generate ./internal/unicodechars
// Set VSCODE_DIR to a checkout of the pinned revision to work offline.

package unicodechars

const vscodeDataCommit = %s

type ambiguousPair struct {
	codePoint rune
	primary   rune
}

type runeRange struct {
	first rune
	last  rune
}

`, vscodeCommit, stringsPath, strconv.Quote(vscodeCommit))

	fmt.Fprintf(&buf, "// Invisible code points compressed into sorted, contiguous ranges.\nvar invisibleRanges = [...]runeRange{\n")
	for _, r := range parsed.invisible {
		fmt.Fprintf(&buf, "\t{0x%04X, 0x%04X},\n", r.first, r.last)
	}
	buf.WriteString("}\n\n")

	locales := make([]string, 0, len(parsed.ambiguous))
	for locale := range parsed.ambiguous {
		locales = append(locales, locale)
	}
	slices.Sort(locales)

	buf.WriteString("// Confusable code points and their primary ASCII equivalents, sorted by code point.\nvar ambiguousData = map[string][]ambiguousPair{\n")
	for _, locale := range locales {
		fmt.Fprintf(&buf, "\t%s: {\n", strconv.Quote(locale))
		for _, pair := range parsed.ambiguous[locale] {
			fmt.Fprintf(&buf, "\t\t{0x%04X, 0x%04X},\n", pair.codePoint, pair.primary)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated file: %w", err)
	}
	return formatted, nil
}
