package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

const maxPreparedBundleBytes = 1 << 20

// preparedBundle is not a reservation or authorization. The future writer
// must retain this exact result in its journal; resume must not reallocate IDs.
type preparedBundle struct {
	RequestID     string
	RequestDigest string
	Cards         []preparedCard
	Batch         intentdoc.Document
	Ledger        idLedger
}

// prepareTaskBundle is pure. The caller supplies a locked, validated snapshot:
// observed+shared/history merged IDs and the registered Intent. Claims,
// pending state, policy/namespace bindings and existing Batch keys must also
// be checked by the writer before it publishes a journal or any reservation.
func prepareTaskBundle(document intentdoc.BundleDocument, entries []Entry, ledger idLedger, registered intentdoc.Document) (preparedBundle, error) {
	req, err := document.Snapshot()
	if err != nil {
		return preparedBundle{}, err
	}
	digest, err := registered.Digest()
	if err != nil {
		return preparedBundle{}, err
	}
	if registered.Kind() != "intent" || registered.ID() != req.Batch.Intent.ID || registered.Revision() != req.Batch.Intent.Revision || digest != req.Batch.Intent.Digest {
		return preparedBundle{}, errors.New("bundle requires its exact registered Intent revision and digest")
	}
	if err := validateGraph(entries); err != nil {
		return preparedBundle{}, err
	}
	next, err := bundleLedger(ledger, entries)
	if err != nil {
		return preparedBundle{}, err
	}
	ids := make(map[string]string, len(req.Tasks))
	// Explicit IDs take precedence over all automatic allocations, even when
	// their draft occurs later. Never mutate caller-owned ledger backing arrays.
	for _, task := range req.Tasks {
		if task.ID == "" {
			continue
		}
		id, err := allocateID(next, task.ID, "TASK")
		if err != nil {
			return preparedBundle{}, err
		}
		ids[task.Key] = id
		next.Reserved = unionIDs(next.Reserved, []string{identityKey(id)})
	}
	for _, task := range req.Tasks {
		if task.ID != "" {
			continue
		}
		id, err := allocateID(next, "", "TASK")
		if err != nil {
			return preparedBundle{}, err
		}
		ids[task.Key] = id
		next.Reserved = unionIDs(next.Reserved, []string{identityKey(id)})
	}
	if _, err := ledgerBytes(next); err != nil {
		return preparedBundle{}, err
	}
	existing := map[string]bool{}
	for _, entry := range entries {
		if isWorkTask(entry.Card.ID) {
			existing[identityKey(entry.Card.ID)] = true
		}
	}
	result := preparedBundle{RequestID: req.RequestID, Ledger: next, Cards: make([]preparedCard, 0, len(req.Tasks))}
	result.RequestDigest, err = document.Digest()
	if err != nil {
		return preparedBundle{}, err
	}
	proposed := append([]Entry(nil), entries...)
	batchIDs := make([]string, 0, len(req.Tasks))
	total := 0
	for _, draft := range req.Tasks {
		create, err := bundleCreateRequest(draft, ids, existing)
		if err != nil {
			return preparedBundle{}, err
		}
		prepared, err := prepareCreatedCard(ids[draft.Key], create)
		if err != nil {
			return preparedBundle{}, fmt.Errorf("draft %s: %w", draft.Key, err)
		}
		total += len(prepared.Raw)
		if total > maxPreparedBundleBytes {
			return preparedBundle{}, errors.New("prepared bundle exceeds 1 MiB")
		}
		result.Cards = append(result.Cards, prepared)
		proposed = append(proposed, prepared.Entry)
		batchIDs = append(batchIDs, prepared.Entry.Card.ID)
	}
	if err := validateGraph(proposed); err != nil {
		return preparedBundle{}, err
	}
	batch := intentdoc.Batch{SchemaVersion: 1, Kind: "batch", ID: req.Batch.ID, Revision: req.Batch.Revision, Intent: req.Batch.Intent, Gap: req.Batch.Gap, TaskIDs: batchIDs, Constraints: req.Batch.Constraints, AuthorizationRefs: req.Batch.AuthorizationRefs}
	raw, err := json.Marshal(batch)
	if err != nil {
		return preparedBundle{}, err
	}
	result.Batch, err = intentdoc.Parse(raw)
	if err != nil {
		return preparedBundle{}, err
	}
	canonical, err := result.Batch.Canonical()
	if err != nil {
		return preparedBundle{}, err
	}
	if total+len(canonical) > maxPreparedBundleBytes {
		return preparedBundle{}, errors.New("prepared bundle exceeds 1 MiB")
	}
	return result, nil
}

func bundleLedger(ledger idLedger, entries []Entry) (idLedger, error) {
	if ledger.SchemaVersion < 1 || ledger.SchemaVersion > 3 || ledger.Reserved == nil {
		return idLedger{}, errors.New("invalid bundle snapshot ID ledger")
	}
	if (ledger.SchemaVersion == 3 && !sharedHex32.MatchString(ledger.Namespace)) || (ledger.SchemaVersion != 3 && ledger.Namespace != "") {
		return idLedger{}, errors.New("invalid bundle snapshot namespace")
	}
	for i, id := range ledger.Reserved {
		if identityKey(id) == "" || identityKey(id) != id || (i > 0 && ledger.Reserved[i-1] >= id) || (ledger.SchemaVersion == 1 && !canonicalID.MatchString(id)) {
			return idLedger{}, errors.New("invalid bundle snapshot reservation")
		}
	}
	observed := make([]string, 0, len(entries))
	for _, entry := range entries {
		observed = append(observed, identityKey(entry.Card.ID))
	}
	ledger.Reserved = unionIDs(ledger.Reserved, observed)
	if ledger.SchemaVersion != 3 {
		ledger.SchemaVersion = 2
	}
	return ledger, nil
}

func bundleCreateRequest(draft intentdoc.TaskDraft, ids map[string]string, existing map[string]bool) (CreateRequest, error) {
	req := CreateRequest{Kind: "task", ID: ids[draft.Key], Title: draft.Title, DependsOn: make([]string, 0, len(draft.DependsOn))}
	for _, ref := range draft.DependsOn {
		id := ref.TaskID
		if ref.Key != "" {
			id = ids[ref.Key]
		} else if !existing[identityKey(id)] {
			return CreateRequest{}, fmt.Errorf("draft %s requires existing TASK %s; use a key for new tasks", draft.Key, id)
		}
		req.DependsOn = append(req.DependsOn, id)
	}
	if draft.Template != nil {
		rules, err := card.ParseValidationConfig([]byte(draft.Template.ValidationConfig))
		if err != nil {
			return CreateRequest{}, fmt.Errorf("draft %s validation config: %w", draft.Key, err)
		}
		req.Template = &CreateTemplate{Rules: rules, Type: draft.Template.Type, Priority: draft.Template.Priority, Summary: draft.Template.Summary, Criteria: append([]string(nil), draft.Template.Criteria...)}
	}
	return req, nil
}
