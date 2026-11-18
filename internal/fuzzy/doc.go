// Package fuzzy ports the quick-open scoring code from VS Code's
// src/vs/base/common/fuzzyScorer.ts, the parts of filters.ts it depends on,
// and the scorer cache, at commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc.
//
// Offsets and ranges are Unicode rune (code point) counts, not UTF-16 code
// units or UTF-8 bytes. A Match covers the half-open range [Start, End), so
// a one-rune highlight is {Start: i, End: i+1}. This matters for labels with
// Chinese or other multibyte characters: a byte offset would point into the
// middle of a rune. Lowercasing is also done rune by rune so the lowercase
// form always has the same length as the original; that keeps scoring and
// highlight positions aligned.
//
// Each call keeps its scoring tables local. VS Code reuses module-level
// scratch arrays because JavaScript is single-threaded; Go callers can run on
// multiple goroutines, so ported algorithms allocate their own state.
package fuzzy
