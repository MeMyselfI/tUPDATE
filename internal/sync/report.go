package sync

import (
	"fmt"
	"strings"
)

// Summary aggregates Counts across multiple DirDiffs.
type Summary struct {
	Added    int
	Modified int
	Removed  int
}

// Summarize totals all changes across the given diffs.
func Summarize(diffs []DirDiff) Summary {
	var s Summary
	for i := range diffs {
		a, m, r := diffs[i].Counts()
		s.Added += a
		s.Modified += m
		s.Removed += r
	}
	return s
}

// HasChanges reports whether any diff has at least one change.
func (s Summary) HasChanges() bool {
	return s.Added+s.Modified+s.Removed > 0
}

type reportRow struct {
	label string
	add   int
	mod   int
	rem   int
}

// FormatReport renders a multi-line, column-aligned diff report.
// Example output:
//
//	Diff:
//	  bin/   :  +3  ~12  -1
//	  www/   :  +0   ~5  -0
//	  etc/   :  +1   ~0  -2
//	Gesamt :  +4  ~12  -3
func FormatReport(diffs []DirDiff) string {
	if len(diffs) == 0 {
		return "Diff: (keine Verzeichnisse)\n"
	}

	rows := make([]reportRow, 0, len(diffs))
	for _, d := range diffs {
		a, m, r := d.Counts()
		rows = append(rows, reportRow{label: d.Dir + "/", add: a, mod: m, rem: r})
	}
	sum := Summarize(diffs)

	labelWidth := len("Gesamt")
	for _, r := range rows {
		if w := len(r.label); w > labelWidth {
			labelWidth = w
		}
	}

	addW := columnWidth("+", rows, sum.Added, func(r reportRow) int { return r.add })
	modW := columnWidth("~", rows, sum.Modified, func(r reportRow) int { return r.mod })
	remW := columnWidth("-", rows, sum.Removed, func(r reportRow) int { return r.rem })

	var b strings.Builder
	b.WriteString("Diff:\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s :  %*s  %*s  %*s\n",
			labelWidth, r.label,
			addW, fmt.Sprintf("+%d", r.add),
			modW, fmt.Sprintf("~%d", r.mod),
			remW, fmt.Sprintf("-%d", r.rem),
		)
	}
	fmt.Fprintf(&b, "%-*s :  %*s  %*s  %*s\n",
		labelWidth+2, "Gesamt",
		addW, fmt.Sprintf("+%d", sum.Added),
		modW, fmt.Sprintf("~%d", sum.Modified),
		remW, fmt.Sprintf("-%d", sum.Removed),
	)
	return b.String()
}

// FormatDetails renders one line per file change in the diff, prefixed with
// an icon indicating the action:
//
//	[+] dir/relpath   -- new file
//	[~] dir/relpath   -- existing file will be overwritten
//	[-] dir/relpath   -- existing file will be removed
//
// Files within each dir are sorted alphabetically; dirs are emitted in the
// order they appear in the slice. Empty diffs return an empty string.
func FormatDetails(diffs []DirDiff) string {
	var b strings.Builder
	for _, d := range diffs {
		if len(d.Changes) == 0 {
			continue
		}
		sorted := make([]Change, len(d.Changes))
		copy(sorted, d.Changes)
		sortChanges(sorted)
		for _, c := range sorted {
			fmt.Fprintf(&b, "%s %s/%s\n", iconFor(c.Type), d.Dir, c.Path)
		}
	}
	return b.String()
}

func iconFor(t ChangeType) string {
	switch t {
	case Added:
		return "[+]"
	case Modified:
		return "[~]"
	case Removed:
		return "[-]"
	}
	return "[?]"
}

// sortChanges orders changes by relative path (lexicographic) for stable output.
func sortChanges(c []Change) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j-1].Path > c[j].Path; j-- {
			c[j-1], c[j] = c[j], c[j-1]
		}
	}
}

func columnWidth(prefix string, rows []reportRow, total int, get func(reportRow) int) int {
	w := len(fmt.Sprintf("%s%d", prefix, total))
	for _, r := range rows {
		if s := fmt.Sprintf("%s%d", prefix, get(r)); len(s) > w {
			w = len(s)
		}
	}
	if w < 2 {
		w = 2
	}
	return w
}
