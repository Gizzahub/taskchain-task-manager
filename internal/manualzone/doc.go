// Package manualzone decides manual-zone entry and exit for a board that has
// explicitly selected the section-11 amendment on a section-9 or section-10
// base profile.
//
// The decisions implement the required meaning of specs/task-file-roles
// section 11, which replaces section 9.7 for that selection: entry, exit,
// and human reissue. They do not activate a public selection wire, add a
// command spelling, or take the common-dir runtime lease. Callers that do
// not hold section-11 must not observe a file change from a refused plan.
package manualzone
