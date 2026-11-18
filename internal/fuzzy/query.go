package fuzzy

import (
	"runtime"
	"strings"
	"unicode"
)

var pathSeparator = func() rune {
	if runtime.GOOS == "windows" {
		return '\\'
	}
	return '/'
}()

// IPreparedQueryPiece corresponds to IPreparedQueryPiece in VS Code's
// fuzzyScorer.ts.
type IPreparedQueryPiece struct {
	// Original is the query as provided by the user.
	Original string
	// OriginalLowercase is Original lowercased.
	OriginalLowercase string
	// PathNormalized has platform path separators (C:\ on Windows, / elsewhere).
	PathNormalized string
	// Normalized removes whitespace, wildcards, quotes, ellipsis and a trailing
	// hash from PathNormalized.
	Normalized string
	// NormalizedLowercase is Normalized lowercased.
	NormalizedLowercase string
	// ExpectContiguousMatch means the query was wrapped in quotes.
	ExpectContiguousMatch bool
}

// IPreparedQuery corresponds to IPreparedQuery in VS Code's fuzzyScorer.ts.
// The embedded piece holds the whole query's normalized forms.
type IPreparedQuery struct {
	IPreparedQueryPiece
	// Values holds the pieces split on spaces; nil when there is one piece.
	Values []IPreparedQueryPiece
	// ContainsPathSeparator reports whether the query has a path separator.
	ContainsPathSeparator bool
}

// PrepareQuery corresponds to prepareQuery. VS Code's overload for a piece is
// replaced by PieceToQuery.
func PrepareQuery(original string) IPreparedQuery {
	originalLowercase := lowerString(original)
	pathNormalized, normalized, normalizedLowercase := normalizeQuery(original)
	containsPathSeparator := strings.ContainsRune(pathNormalized, pathSeparator)
	expectExactMatch := queryExpectsExactMatch(original)

	var values []IPreparedQueryPiece
	for piece := range strings.SplitSeq(original, " ") {
		piecePathNormalized, pieceNormalized, pieceNormalizedLowercase := normalizeQuery(piece)
		if pieceNormalized == "" {
			continue
		}
		if values == nil {
			values = make([]IPreparedQueryPiece, 0)
		}
		values = append(values, IPreparedQueryPiece{
			Original:              piece,
			OriginalLowercase:     lowerString(piece),
			PathNormalized:        piecePathNormalized,
			Normalized:            pieceNormalized,
			NormalizedLowercase:   pieceNormalizedLowercase,
			ExpectContiguousMatch: queryExpectsExactMatch(piece),
		})
	}

	return IPreparedQuery{
		IPreparedQueryPiece: IPreparedQueryPiece{
			Original:              original,
			OriginalLowercase:     originalLowercase,
			PathNormalized:        pathNormalized,
			Normalized:            normalized,
			NormalizedLowercase:   normalizedLowercase,
			ExpectContiguousMatch: expectExactMatch,
		},
		Values:                values,
		ContainsPathSeparator: containsPathSeparator,
	}
}

// PieceToQuery corresponds to pieceToQuery for a single IPreparedQueryPiece.
func PieceToQuery(piece IPreparedQueryPiece) IPreparedQuery {
	return PrepareQuery(piece.Original)
}

// PiecesToQuery corresponds to pieceToQuery for an array of pieces.
func PiecesToQuery(pieces []IPreparedQueryPiece) IPreparedQuery {
	originals := make([]string, len(pieces))
	for i, piece := range pieces {
		originals[i] = piece.Original
	}
	return PrepareQuery(strings.Join(originals, " "))
}

func queryExpectsExactMatch(query string) bool {
	return strings.HasPrefix(query, `"`) && strings.HasSuffix(query, `"`)
}

func normalizeQuery(original string) (pathNormalized, normalized, normalizedLowercase string) {
	if pathSeparator == '\\' {
		pathNormalized = strings.ReplaceAll(original, "/", `\`)
	} else {
		pathNormalized = strings.ReplaceAll(original, `\`, "/")
	}

	var builder strings.Builder
	for _, r := range pathNormalized {
		if r == '*' || r == '…' || r == '"' || unicode.IsSpace(r) {
			continue
		}
		builder.WriteRune(r)
	}
	normalized = builder.String()
	// JS: /(?<=.)#$/, i.e. a trailing hash is dropped only when something
	// precedes it in the normalized query.
	if strings.HasSuffix(normalized, "#") && runeLen(normalized) > 1 {
		normalized = strings.TrimSuffix(normalized, "#")
	}
	return pathNormalized, normalized, lowerString(normalized)
}
