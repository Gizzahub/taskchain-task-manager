package taskstore

// ListReadOnly returns the normal card projection without acquiring the board
// writer lock or creating any lock directory. Callers that need a stable
// observation must bracket it with their own identity/content check.
func ListReadOnly(dir string) (entries []Entry, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := rejectPendingTransitions(r); err != nil {
		return nil, err
	}
	entries, err = listLocked(r)
	if err != nil {
		return nil, err
	}
	if _, err := loadClaims(r, entries); err != nil {
		return nil, err
	}
	return entries, nil
}
