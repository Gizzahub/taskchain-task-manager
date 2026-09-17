package taskstore

// Schema 2 preparation can hold the worst-case canonical encoding of any
// valid schema 1 input after namespace addition: six bytes per input byte,
// plus the canonical newline. The field set must remain unchanged.
const maxArchiveCapacityBytes = 6*maxRepairsBytes + 1

func archiveJournalVersionLimit(schema int) int {
	switch schema {
	case 1:
		return maxRepairsBytes
	case 2:
		return maxArchiveCapacityBytes
	default:
		return 0
	}
}

// These pure preparation functions do not authorize a filesystem format
// upgrade. Existing readers/writers deliberately still reject schema 2 until
// protocol 5 adoption and its exact-byte recovery transaction are connected.
func decodeArchiveCapacityJournal(raw []byte) (archiveJournal, error) {
	return decodeArchiveJournalVersion(raw, 2)
}

func archiveCapacityJournalBytes(j archiveJournal) ([]byte, error) {
	return archiveJournalBytesVersion(j, 2)
}
