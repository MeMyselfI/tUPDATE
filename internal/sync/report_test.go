package sync

import (
	"strings"
	"testing"
)

func TestSummarize_AggregatesAllCounts(t *testing.T) {
	diffs := []DirDiff{
		{Dir: "bin", Changes: []Change{
			{Path: "a", Type: Added},
			{Path: "b", Type: Modified},
			{Path: "c", Type: Modified},
			{Path: "d", Type: Removed},
		}},
		{Dir: "www", Changes: []Change{
			{Path: "e", Type: Modified},
		}},
		{Dir: "etc", Changes: nil},
	}
	got := Summarize(diffs)
	if got.Added != 1 {
		t.Errorf("Added = %d, want 1", got.Added)
	}
	if got.Modified != 3 {
		t.Errorf("Modified = %d, want 3", got.Modified)
	}
	if got.Removed != 1 {
		t.Errorf("Removed = %d, want 1", got.Removed)
	}
}

func TestSummary_HasChanges(t *testing.T) {
	if (Summary{}).HasChanges() {
		t.Error("zero summary should report no changes")
	}
	if !(Summary{Modified: 1}).HasChanges() {
		t.Error("Modified=1 should report changes")
	}
}

func TestFormatReport_ContainsAllDirsAndTotals(t *testing.T) {
	diffs := []DirDiff{
		{Dir: "bin", Changes: []Change{
			{Path: "a", Type: Added}, {Path: "b", Type: Added}, {Path: "c", Type: Added},
			{Path: "m1", Type: Modified}, {Path: "m2", Type: Modified},
			{Path: "r1", Type: Removed},
		}},
		{Dir: "www", Changes: []Change{
			{Path: "i1", Type: Modified}, {Path: "i2", Type: Modified},
		}},
		{Dir: "etc", Changes: nil},
		{Dir: "libs", Changes: []Change{
			{Path: "old.jar", Type: Removed},
		}},
	}
	out := FormatReport(diffs)

	for _, expected := range []string{"bin/", "www/", "etc/", "libs/", "Gesamt"} {
		if !strings.Contains(out, expected) {
			t.Errorf("output missing %q\n%s", expected, out)
		}
	}
	for _, expected := range []string{"+3", "+0", "+0", "+0", "~2", "~2", "~0", "~0", "-1", "-0", "-0", "-1"} {
		if !strings.Contains(out, expected) {
			t.Errorf("output missing token %q\n%s", expected, out)
		}
	}
	// Total: +3 ~4 -2
	if !strings.Contains(out, "+3") || !strings.Contains(out, "~4") || !strings.Contains(out, "-2") {
		t.Errorf("totals wrong\n%s", out)
	}
}

func TestFormatReport_EmptyDiffs(t *testing.T) {
	out := FormatReport(nil)
	if !strings.Contains(out, "keine Verzeichnisse") {
		t.Errorf("empty report unexpected: %q", out)
	}
}

func TestFormatReport_AllZerosStillRenders(t *testing.T) {
	diffs := []DirDiff{
		{Dir: "bin"},
		{Dir: "www"},
	}
	out := FormatReport(diffs)
	if !strings.Contains(out, "bin/") || !strings.Contains(out, "Gesamt") {
		t.Errorf("zero diff not rendered: %q", out)
	}
	if !strings.Contains(out, "+0") || !strings.Contains(out, "~0") || !strings.Contains(out, "-0") {
		t.Errorf("expected +0/~0/-0 cells: %q", out)
	}
}

func TestFormatReport_LongDirNameAligned(t *testing.T) {
	diffs := []DirDiff{
		{Dir: "very-long-directory-name", Changes: []Change{{Path: "x", Type: Added}}},
		{Dir: "x", Changes: []Change{{Path: "y", Type: Removed}}},
	}
	out := FormatReport(diffs)
	if !strings.Contains(out, "very-long-directory-name/") {
		t.Errorf("long name missing: %q", out)
	}
	if !strings.Contains(out, "x/") {
		t.Errorf("short name missing: %q", out)
	}
}
