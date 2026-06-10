// Package merge implements a line-level three-way text merge.
package merge

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Result is returned by Merge3.
type Result struct {
	Text        string // merged content
	HasConflict bool   // true if any conflict markers were written
}

// Merge3 merges base, ours, and theirs line by line.
// Non-overlapping changes are applied automatically; overlapping changes
// produce git-style conflict markers.
func Merge3(base, ours, theirs string) Result {
	baseLines := splitLines(base)
	ourLines := splitLines(ours)
	theirLines := splitLines(theirs)

	ourOps := opcodes(baseLines, ourLines)
	theirOps := opcodes(baseLines, theirLines)

	var sb strings.Builder
	hasConflict := false

	i := 0  // current position in base
	oi := 0 // next index into ourOps
	ti := 0 // next index into theirOps

	for i < len(baseLines) || oi < len(ourOps) || ti < len(theirOps) {
		oOp := peekOp(ourOps, oi, i, len(baseLines))
		tOp := peekOp(theirOps, ti, i, len(baseLines))

		oEqual := oOp.Tag == 'e'
		tEqual := tOp.Tag == 'e'

		// Both sides equal at position i — output base and advance.
		if oEqual && tEqual {
			end := min3(oOp.I2, tOp.I2, len(baseLines))
			for j := i; j < end; j++ {
				sb.WriteString(baseLines[j])
			}
			i = end
			// Advance iterators past equal ops that are fully consumed.
			advancePast(&oi, ourOps, i)
			advancePast(&ti, theirOps, i)
			continue
		}

		// Only ours changed.
		if !oEqual && tEqual {
			for _, l := range ourLines[oOp.J1:oOp.J2] {
				sb.WriteString(l)
			}
			i = oOp.I2
			oi++
			advancePast(&ti, theirOps, i)
			continue
		}

		// Only theirs changed.
		if oEqual && !tEqual {
			for _, l := range theirLines[tOp.J1:tOp.J2] {
				sb.WriteString(l)
			}
			i = tOp.I2
			ti++
			advancePast(&oi, ourOps, i)
			continue
		}

		// Both changed — conflict.
		oText := strings.Join(ourLines[oOp.J1:oOp.J2], "")
		tText := strings.Join(theirLines[tOp.J1:tOp.J2], "")
		if oText == tText {
			sb.WriteString(oText) // identical change on both sides — no conflict
		} else {
			hasConflict = true
			sb.WriteString("<<<<<<< ours\n")
			sb.WriteString(oText)
			sb.WriteString("=======\n")
			sb.WriteString(tText)
			sb.WriteString(">>>>>>> theirs\n")
		}
		i = max2(oOp.I2, tOp.I2)
		oi++
		ti++
	}

	return Result{Text: sb.String(), HasConflict: hasConflict}
}

// HasConflictMarkers reports whether text contains unresolved conflict markers.
func HasConflictMarkers(text string) bool {
	return strings.Contains(text, "<<<<<<< ours\n") &&
		strings.Contains(text, "=======\n") &&
		strings.Contains(text, ">>>>>>> theirs\n")
}

// op is an opcode with a byte tag: 'e'qual, 'r'eplace, 'd'elete, 'i'nsert.
type op struct {
	Tag    byte
	I1, I2 int // range in base (a)
	J1, J2 int // range in comparison (b)
}

func opcodes(a, b []string) []op {
	sm := difflib.NewMatcher(a, b)
	raw := sm.GetOpCodes()
	out := make([]op, len(raw))
	for i, o := range raw {
		out[i] = op{Tag: o.Tag, I1: o.I1, I2: o.I2, J1: o.J1, J2: o.J2}
	}
	return out
}

// peekOp returns a synthetic 'e'qual op covering [i, baseLen) if the next real op
// doesn't start at i yet, or the actual next op if it starts at or before i.
func peekOp(ops []op, idx, i, baseLen int) op {
	// Skip ops fully behind i.
	for idx < len(ops) && consumedBy(ops[idx], i) {
		idx++
	}
	if idx >= len(ops) {
		return op{Tag: 'e', I1: i, I2: baseLen, J1: -1, J2: -1}
	}
	o := ops[idx]
	if o.I1 > i {
		// Gap before next op — treat as equal.
		return op{Tag: 'e', I1: i, I2: o.I1, J1: -1, J2: -1}
	}
	return o
}

// advancePast advances idx past any op fully behind base position i.
func advancePast(idx *int, ops []op, i int) {
	for *idx < len(ops) && consumedBy(ops[*idx], i) {
		*idx++
	}
}

// consumedBy reports whether op lies entirely behind base position i and so has
// already been handled. A zero-width insert (I1==I2) is positioned AT I1, so it is
// only consumed once i has moved strictly past it — otherwise an insertion at the
// cursor (e.g. into an empty base, or before the first line) would be skipped and
// its content silently dropped.
func consumedBy(o op, i int) bool {
	if o.I1 == o.I2 { // insert
		return o.I1 < i
	}
	return o.I2 <= i
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
