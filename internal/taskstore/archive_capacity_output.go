package taskstore

import "github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"

// These describe the public ArchiveCapacityResult document. They intentionally
// do not name the independently versioned values persisted in the adoption
// journal.
const (
	archiveCapacityOutputStorageProtocol = 5
	archiveCapacityOutputJournalSchema   = 2
)

type ArchiveCapacityResult struct {
	UpgradeID       string                   `json:"upgradeId"`
	Status          outputvocab.ResultStatus `json:"status"`
	StorageProtocol int                      `json:"storageProtocol"`
	JournalSchema   int                      `json:"journalSchema"`
}

func completedArchiveCapacityResult(upgradeID string) ArchiveCapacityResult {
	return ArchiveCapacityResult{
		UpgradeID:       upgradeID,
		Status:          outputvocab.Completed,
		StorageProtocol: archiveCapacityOutputStorageProtocol,
		JournalSchema:   archiveCapacityOutputJournalSchema,
	}
}
