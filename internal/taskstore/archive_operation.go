package taskstore

// Persisted archive journal operation spellings. Writers store these exact
// literals in archiveRecord.Operation. Stdout names the same four spellings
// through outputvocab, but that package does not own these bytes: renaming a
// stdout constant does not change a journal.
const (
	diskArchiveOperationArchive        = "archive"
	diskArchiveOperationSupersede      = "supersede"
	diskArchiveOperationForce          = "force"
	diskArchiveOperationLegacyAdoption = "legacy-adoption"
)
