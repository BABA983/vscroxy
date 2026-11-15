package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// captureDiff renders the changes between two captured texts as a unified
// diff with three lines of context. It reads the same hunk model the viewer
// draws, so the exported file marks the changes the reader saw; lines inside
// a hunk are matched with a bounded LCS so replacements stay readable.
func captureDiff(oldName, newName, oldText, newText string) string {
	oldLines, newLines := diffLines(oldText), diffLines(newText)
	state := &editor.DiffState{Old: editor.NewBuffer(oldText), New: editor.NewBuffer(newText)}
	hunks := state.Hunks()
	if len(hunks) == 0 {
		return ""
	}
	const context = 3
	type span struct{ a0, a1, b0, b1 int }
	var spans []span
	for _, h := range hunks {
		next := span{
			a0: max(0, h.OldStart-context), a1: min(len(oldLines), h.OldEnd+context),
			b0: max(0, h.NewStart-context), b1: min(len(newLines), h.NewEnd+context),
		}
		if len(spans) > 0 && next.a0 <= spans[len(spans)-1].a1 {
			spans[len(spans)-1].a1 = max(spans[len(spans)-1].a1, next.a1)
			spans[len(spans)-1].b1 = max(spans[len(spans)-1].b1, next.b1)
			continue
		}
		spans = append(spans, next)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", oldName, newName)
	for _, s := range spans {
		a, b := oldLines[s.a0:s.a1], newLines[s.b0:s.b1]
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(s.a0, len(a)), hunkRange(s.b0, len(b)))
		for _, line := range matchLines(a, b) {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// diffLines splits text into lines without their line feeds. A final line
// feed ends the last line rather than starting an empty one.
func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hunkRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// matchLines orders two hunk sides with their common lines in place. A hunk
// from the viewer is small in ordinary captures; when both sides are large
// the whole old side is removed before the whole new side, which still
// applies but does not try to align thousands of lines.
func matchLines(oldLines, newLines []string) []string {
	const limit = 400
	if len(oldLines) > limit || len(newLines) > limit {
		out := make([]string, 0, len(oldLines)+len(newLines))
		for _, line := range oldLines {
			out = append(out, "-"+line)
		}
		for _, line := range newLines {
			out = append(out, "+"+line)
		}
		return out
	}
	table := make([][]int, len(oldLines)+1)
	for i := range table {
		table[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	out := make([]string, 0, len(oldLines)+len(newLines))
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			out = append(out, " "+oldLines[i])
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			out = append(out, "-"+oldLines[i])
			i++
		default:
			out = append(out, "+"+newLines[j])
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		out = append(out, "-"+oldLines[i])
	}
	for ; j < len(newLines); j++ {
		out = append(out, "+"+newLines[j])
	}
	return out
}
