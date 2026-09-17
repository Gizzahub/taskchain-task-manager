package taskstore

import (
	"errors"
	"os"
)

// archiveActivationBinding is deliberately small: journals remain in inert
// immutable artifacts rather than inflating common or policy JSON.
type archiveActivationBinding struct {
	SchemaVersion         int    `json:"schemaVersion"`
	StorageProtocol       int    `json:"storageProtocol"`
	JournalSchema         int    `json:"journalSchema"`
	JournalMode           uint32 `json:"journalMode"`
	OriginalJournalSHA256 string `json:"originalJournalSha256"`
	TargetJournalSHA256   string `json:"targetJournalSha256"`
	RebindArtifactSHA256  string `json:"rebindArtifactSha256,omitempty"`
	CapacityReceiptSHA256 string `json:"capacityReceiptSha256"`
	CapacityPayloadSHA256 string `json:"capacityPayloadSha256"`
}

func validateArchiveActivationBinding(b *archiveActivationBinding) error {
	if b == nil {
		return nil
	}
	if b.SchemaVersion != 1 || b.StorageProtocol != 5 || b.JournalSchema != 2 || b.JournalMode == 0 || b.JournalMode&^0777 != 0 || !sharedHex64.MatchString(b.OriginalJournalSHA256) || !sharedHex64.MatchString(b.TargetJournalSHA256) || !sharedHex64.MatchString(b.CapacityReceiptSHA256) || !sharedHex64.MatchString(b.CapacityPayloadSHA256) {
		return errors.New("invalid archive activation binding")
	}
	if (b.OriginalJournalSHA256 == b.TargetJournalSHA256) != (b.RebindArtifactSHA256 == "") {
		return errors.New("archive activation artifact must exist exactly when journal changes")
	}
	if b.RebindArtifactSHA256 != "" && !sharedHex64.MatchString(b.RebindArtifactSHA256) {
		return errors.New("invalid archive activation artifact digest")
	}
	return nil
}

// prepareArchiveActivationBinding performs the protocol-5 prerequisite check.
// Capacity evidence proves the format upgrade only; it never selects the bytes
// subsequently activated by a namespace transaction.
func prepareArchiveActivationBinding(r *os.Root, transitions transitionJournal, namespace string) (*archiveActivationBinding, error) {
	if transitions.StorageProtocol != 5 {
		return nil, nil
	}
	capacity, err := validateCompletedArchiveCapacity(r, transitions)
	if err != nil {
		return nil, err
	}
	info, err := r.Lstat(archivesFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() == 0 {
		return nil, errors.New("live archive mode invalid")
	}
	j, live, board := capacity.journal, capacity.live, capacity.board
	if j.Namespace != "" && j.Namespace != namespace {
		return nil, errors.New("live archive scope is not eligible for namespace activation")
	}
	plan, err := PlanArchiveNamespaceRebind(live, bytesDigest(live), board, j.Namespace, namespace, uint32(info.Mode().Perm()))
	if err != nil {
		return nil, err
	}
	if err := verifyArchiveRebindInventoryAtRoot(r, plan); err != nil {
		return nil, err
	}
	b := &archiveActivationBinding{SchemaVersion: 1, StorageProtocol: 5, JournalSchema: 2, JournalMode: uint32(info.Mode().Perm()), OriginalJournalSHA256: plan.OriginalJournalSHA256, TargetJournalSHA256: plan.TargetJournalSHA256, CapacityReceiptSHA256: bytesDigest(capacity.receiptRaw), CapacityPayloadSHA256: capacity.adoption.PayloadSHA256}
	if b.OriginalJournalSHA256 != b.TargetJournalSHA256 {
		b.RebindArtifactSHA256, err = publishArchiveRebindArtifact(r, plan)
		if err != nil {
			return nil, err
		}
	}
	return b, validateArchiveActivationBinding(b)
}

func verifyArchiveActivationBinding(r *os.Root, transitions transitionJournal, namespace string, b *archiveActivationBinding, completed bool) error {
	if transitions.StorageProtocol != 5 {
		return validateArchiveActivationBinding(b)
	}
	if b == nil {
		return errors.New("protocol 5 activation lacks archive binding")
	}
	if err := validateArchiveActivationBinding(b); err != nil {
		return err
	}
	capacity, err := validateCompletedArchiveCapacity(r, transitions)
	if err != nil {
		return err
	}
	if bytesDigest(capacity.receiptRaw) != b.CapacityReceiptSHA256 || capacity.adoption.PayloadSHA256 != b.CapacityPayloadSHA256 {
		return errors.New("archive capacity receipt changed")
	}
	var plan ArchiveNamespaceRebindPlan
	if b.RebindArtifactSHA256 != "" {
		plan, err = loadArchiveRebindArtifact(r, b.RebindArtifactSHA256)
		if err != nil || plan.OriginalJournalSHA256 != b.OriginalJournalSHA256 || plan.TargetJournalSHA256 != b.TargetJournalSHA256 || plan.TargetNamespace != namespace || plan.JournalMode != b.JournalMode {
			return errors.New("archive rebind artifact mismatch")
		}
	}
	if completed {
		if capacity.journal.Namespace != namespace {
			return errors.New("live archive namespace changed")
		}
		return nil
	} // later completed archive writes are valid.
	info, err := r.Lstat(archivesFile)
	if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != b.JournalMode {
		return errors.New("archive journal mode changed")
	}
	h := bytesDigest(capacity.live)
	if h != b.OriginalJournalSHA256 && h != b.TargetJournalSHA256 {
		return errors.New("archive journal is a third state")
	}
	if b.RebindArtifactSHA256 != "" {
		if h == b.OriginalJournalSHA256 && capacity.journal.Namespace != plan.SourceNamespace {
			return errors.New("archive original namespace changed")
		}
		if h == b.TargetJournalSHA256 && capacity.journal.Namespace != plan.TargetNamespace {
			return errors.New("archive target namespace changed")
		}
	} else {
		if capacity.journal.Namespace != namespace {
			return errors.New("archive same-state namespace changed")
		}
		plan, err = PlanArchiveNamespaceRebind(capacity.live, h, capacity.board, namespace, namespace, b.JournalMode)
		if err != nil {
			return err
		}
	}
	return verifyArchiveRebindInventoryAtRoot(r, plan)
}

func publishArchiveActivationTarget(r *os.Root, b *archiveActivationBinding) error {
	if b == nil || b.OriginalJournalSHA256 == b.TargetJournalSHA256 {
		return nil
	}
	plan, err := loadArchiveRebindArtifact(r, b.RebindArtifactSHA256)
	if err != nil {
		return err
	}
	if bytesDigest(plan.TargetJournal) != b.TargetJournalSHA256 {
		return errors.New("archive rebind target mismatch")
	}
	return publishArchiveCapacityTarget(r, plan.TargetJournal, b.JournalMode, b.TargetJournalSHA256)
}

func verifyArchiveRebindInventoryAtRoot(r *os.Root, plan ArchiveNamespaceRebindPlan) error {
	cards := make(map[string]ArchiveNamespaceCard, len(plan.Cards))
	for _, binding := range plan.Cards {
		raw, err := readTransitionCard(r, binding.Path)
		if err != nil {
			return err
		}
		info, err := r.Lstat(binding.Path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive rebind card type changed")
		}
		cards[binding.Path] = ArchiveNamespaceCard{Path: binding.Path, ID: binding.ID, Raw: raw, Mode: uint32(info.Mode().Perm())}
	}
	return VerifyArchiveNamespaceRebindInventory(plan, cards)
}
