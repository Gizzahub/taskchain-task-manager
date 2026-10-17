package boardfiles

import (
	"fmt"
	"slices"
	"sort"
)

// LiveCardPathDelta describes exact path membership changes, not card identity
// or content changes. A move is one removal and one addition.
type LiveCardPathDelta struct {
	Added   []string
	Removed []string
}

// DiffLiveCardPaths compares caller-supplied complete live-path snapshots.
// Callers must obtain membership from the scanner's classification rules and
// reject unavailable or structurally invalid snapshots before calling. This
// function validates projection representability and uniqueness only; it does
// not prove completeness, classify archive/private paths, read files, bind a
// policy authority, or perform writer admission. Inputs are never modified.
func DiffLiveCardPaths(before, after []string) (LiveCardPathDelta, error) {
	left, err := canonicalLiveCardPaths(before)
	if err != nil {
		return LiveCardPathDelta{}, fmt.Errorf("before inventory: %w", err)
	}
	right, err := canonicalLiveCardPaths(after)
	if err != nil {
		return LiveCardPathDelta{}, fmt.Errorf("after inventory: %w", err)
	}
	delta := LiveCardPathDelta{Added: []string{}, Removed: []string{}}
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i] < right[j]:
			delta.Removed = append(delta.Removed, left[i])
			i++
		case left[i] > right[j]:
			delta.Added = append(delta.Added, right[j])
			j++
		default:
			i++
			j++
		}
	}
	delta.Removed = append(delta.Removed, left[i:]...)
	delta.Added = append(delta.Added, right[j:]...)
	return delta, nil
}

func canonicalLiveCardPaths(paths []string) ([]string, error) {
	ordered := slices.Clone(paths)
	sort.Strings(ordered) // Same UTF-8 byte ordering as ScanRoot's projection.
	for i, rel := range ordered {
		if issue := projectionPathIssue(rel); issue != "" {
			return nil, fmt.Errorf("%q: %s", rel, issue)
		}
		if i > 0 && rel == ordered[i-1] {
			return nil, fmt.Errorf("duplicate live card path %q", rel)
		}
	}
	return ordered, nil
}
