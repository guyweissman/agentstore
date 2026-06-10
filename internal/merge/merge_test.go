package merge_test

import (
	"strings"
	"testing"

	"github.com/guyweissman/agentstore/internal/merge"
)

func TestNoConflict_OnlyOursChanged(t *testing.T) {
	base := "line 1\nline 2\nline 3\n"
	ours := "line 1\nline 2 changed\nline 3\n"
	theirs := base

	r := merge.Merge3(base, ours, theirs)
	if r.HasConflict {
		t.Error("expected no conflict")
	}
	if r.Text != ours {
		t.Errorf("got %q, want %q", r.Text, ours)
	}
}

func TestNoConflict_OnlyTheirsChanged(t *testing.T) {
	base := "line 1\nline 2\nline 3\n"
	ours := base
	theirs := "line 1\nline 2\nline 3 changed\n"

	r := merge.Merge3(base, ours, theirs)
	if r.HasConflict {
		t.Error("expected no conflict")
	}
	if r.Text != theirs {
		t.Errorf("got %q, want %q", r.Text, theirs)
	}
}

func TestNoConflict_BothChangedDifferentLines(t *testing.T) {
	base := "line 1\nline 2\nline 3\n"
	ours := "line 1 ours\nline 2\nline 3\n"
	theirs := "line 1\nline 2\nline 3 theirs\n"

	r := merge.Merge3(base, ours, theirs)
	if r.HasConflict {
		t.Error("expected no conflict for non-overlapping changes")
	}
	if !strings.Contains(r.Text, "line 1 ours") {
		t.Error("ours change should appear in merged output")
	}
	if !strings.Contains(r.Text, "line 3 theirs") {
		t.Error("theirs change should appear in merged output")
	}
}

func TestConflict_BothChangedSameLine(t *testing.T) {
	base := "line 1\nline 2\nline 3\n"
	ours := "line 1\nOURS VERSION\nline 3\n"
	theirs := "line 1\nTHEIRS VERSION\nline 3\n"

	r := merge.Merge3(base, ours, theirs)
	if !r.HasConflict {
		t.Error("expected conflict for overlapping changes")
	}
	if !strings.Contains(r.Text, "<<<<<<< ours") {
		t.Error("expected conflict markers in output")
	}
	if !strings.Contains(r.Text, "OURS VERSION") {
		t.Error("expected ours content in markers")
	}
	if !strings.Contains(r.Text, "THEIRS VERSION") {
		t.Error("expected theirs content in markers")
	}
}

func TestBothUnchanged(t *testing.T) {
	content := "line 1\nline 2\n"
	r := merge.Merge3(content, content, content)
	if r.HasConflict {
		t.Error("expected no conflict when all three are identical")
	}
	if r.Text != content {
		t.Errorf("got %q, want %q", r.Text, content)
	}
}

func TestNewFile_BothAdded_Identical(t *testing.T) {
	// Both sides add the same content to an empty base.
	content := "new content\n"
	r := merge.Merge3("", content, content)
	if r.HasConflict {
		t.Error("expected no conflict when both add identical content")
	}
	// Must also produce the content — a pure insertion into an empty base must
	// not be dropped (regression: this previously returned "").
	if r.Text != content {
		t.Errorf("both-added-identical should yield the content, got %q", r.Text)
	}
}

func TestInsertIntoEmptyBase(t *testing.T) {
	// Only one side adds content to an empty file — take it cleanly.
	if r := merge.Merge3("", "hello\n", ""); r.HasConflict || r.Text != "hello\n" {
		t.Errorf("insert by ours into empty base: got %q conflict=%v", r.Text, r.HasConflict)
	}
	// Prepend a line to a non-empty base.
	if r := merge.Merge3("b\n", "a\nb\n", "b\n"); r.HasConflict || r.Text != "a\nb\n" {
		t.Errorf("prepend by ours: got %q conflict=%v", r.Text, r.HasConflict)
	}
}

func TestHasConflictMarkers(t *testing.T) {
	clean := "just text\n"
	if merge.HasConflictMarkers(clean) {
		t.Error("clean text should not have conflict markers")
	}
	conflicted := "<<<<<<< ours\nours\n=======\ntheirs\n>>>>>>> theirs\n"
	if !merge.HasConflictMarkers(conflicted) {
		t.Error("conflicted text should have markers detected")
	}
}

// FuzzMerge3 checks the core three-way merge invariants on arbitrary inputs:
//   - if ours == theirs, the merge is that text, conflict-free;
//   - if only one side changed (the other equals base), the merge takes the
//     changed side, conflict-free.
//
// A counterexample is a real merge bug (silent corruption or a spurious conflict).
func FuzzMerge3(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nB\nc\n", "a\nb\nC\n")
	f.Add("", "x\n", "y\n")
	f.Add("l1\nl2\n", "l1\nl2\n", "l1\nl2\n")
	f.Add("a\n", "", "a\n")

	f.Fuzz(func(t *testing.T, base, ours, theirs string) {
		// Identical sides: result is that text, no conflict.
		if ours == theirs {
			r := merge.Merge3(base, ours, theirs)
			if r.HasConflict || r.Text != ours {
				t.Errorf("ours==theirs should merge cleanly to ours\nbase=%q ours=%q got=%q conflict=%v",
					base, ours, r.Text, r.HasConflict)
			}
		}
		// Only theirs changed: take theirs cleanly.
		if ours == base {
			r := merge.Merge3(base, ours, theirs)
			if r.HasConflict || r.Text != theirs {
				t.Errorf("only theirs changed should take theirs\nbase=%q theirs=%q got=%q conflict=%v",
					base, theirs, r.Text, r.HasConflict)
			}
		}
		// Only ours changed: take ours cleanly.
		if theirs == base {
			r := merge.Merge3(base, ours, theirs)
			if r.HasConflict || r.Text != ours {
				t.Errorf("only ours changed should take ours\nbase=%q ours=%q got=%q conflict=%v",
					base, ours, r.Text, r.HasConflict)
			}
		}
	})
}
