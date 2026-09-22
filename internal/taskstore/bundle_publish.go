package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type BundleOptions struct{ Adopt, Resume bool }

type BundleTaskResult struct {
	Key  string `json:"key"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type BundleResult struct {
	RequestID string                   `json:"requestId"`
	Digest    string                   `json:"digest"`
	Status    outputvocab.ResultStatus `json:"status"`
	Replayed  bool                     `json:"replayed"`
	Tasks     []BundleTaskResult       `json:"tasks"`
	Batch     json.RawMessage          `json:"batch"`
}

// PublishBundle records one immutable allocation and publishes its cards and
// Batch. Pending publication is visible only to explicit same-request resume.
func PublishBundle(dir string, raw []byte, options BundleOptions) (BundleResult, error) {
	return publishBundleWithStep(dir, raw, options, nil)
}

func publishBundleWithStep(dir string, raw []byte, options BundleOptions, step func(string) error) (result BundleResult, err error) {
	document, err := intentdoc.ParseBundle(raw)
	if err != nil {
		return result, err
	}
	req, err := document.Snapshot()
	if err != nil {
		return result, err
	}
	canonical, err := document.Canonical()
	if err != nil {
		return result, err
	}
	s, err := openBundleSession(dir)
	if err != nil {
		return result, err
	}
	mayHavePublished := false
	defer func() {
		err = errors.Join(err, s.close())
		if err != nil && mayHavePublished {
			err = fmt.Errorf("bundle %s publication outcome may be uncertain; preserve its journal and retry identical input (use explicit resume if a pending record is reported; without a recorded transaction retry the original invocation): %w", req.RequestID, err)
		}
	}()
	for index, record := range s.journal.Records {
		if record.RequestID != req.RequestID {
			continue
		}
		if !bytes.Equal(record.Request, canonical) {
			return result, errors.New("bundle request ID already binds different immutable content")
		}
		if record.Status == "completed" {
			for _, other := range s.journal.Records {
				if other.Status == "pending" {
					return result, errors.New("another pending bundle must be recovered first")
				}
			}
			// A historical local receipt need not retain today's namespace or
			// graph. Only a still-pending common receipt needs owner cleanup.
			if s.shared != nil && s.shared.state != nil && s.shared.state.PendingBundle != nil {
				if err := s.shared.finishBundle(s.root, s.journal, record); err != nil {
					return result, err
				}
			}
			mayHavePublished = true
			return bundleResult(record, true), nil
		}
		if !options.Resume {
			return result, errors.New("pending bundle requires the same request with explicit resume")
		}
		mayHavePublished = true
		if err := s.publishRecord(index, step); err != nil {
			return result, err
		}
		return bundleResult(s.journal.Records[index], true), nil
	}
	if options.Resume {
		return result, errors.New("bundle request has no recorded transaction to resume")
	}
	record, err := s.prepareRecord(document)
	if err != nil {
		return result, err
	}
	if err := s.adopt(options.Adopt, step); err != nil {
		return result, err
	}
	s.journal.Records = append(s.journal.Records, record)
	mayHavePublished = true
	if err := saveBundles(s.root, s.journal, false); err != nil {
		return result, err
	}
	if err := bundleStep(step, "after-pending-journal"); err != nil {
		return result, err
	}
	index := len(s.journal.Records) - 1
	if err := s.publishRecord(index, step); err != nil {
		return result, err
	}
	return bundleResult(s.journal.Records[index], false), nil
}

func bundleResult(record bundleRecord, replayed bool) BundleResult {
	result := BundleResult{RequestID: record.RequestID, Digest: record.RequestDigest,
		Status: outputvocab.ResultStatus(record.Status), Replayed: replayed, Tasks: []BundleTaskResult{}, Batch: append(json.RawMessage(nil), record.Batch...)}
	for _, card := range record.Cards {
		result.Tasks = append(result.Tasks, BundleTaskResult{card.Key, card.ID, card.Path})
	}
	return result
}

func (s *bundleSession) publishRecord(index int, step func(string) error) error {
	record := s.journal.Records[index]
	if err := s.validateResume(record); err != nil {
		return err
	}
	if err := s.shared.reserveBundle(s.root, s.journal, record); err != nil {
		return err
	}
	if err := bundleStep(step, "after-shared-reservation"); err != nil {
		return err
	}
	current, err := boundedSnapshotFile(s.root, idsFile, maxIDsBytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, record.TargetIDs) {
		ledger, err := decodeIDs(record.TargetIDs)
		if err != nil {
			return err
		}
		if err := publishValidatedIDs(s.root, ledger, false); err != nil {
			return err
		}
	}
	if err := bundleStep(step, "after-local-reservation"); err != nil {
		return err
	}
	for i, card := range record.Cards {
		if err := relocationParents(s.root, card.Path, true); err != nil {
			return err
		}
		if err := validateBundleDestination(s.root, card.Path, s.policy); err != nil {
			return err
		}
		if err := publishBundleFile(s.root, card.Path, card.Raw); err != nil {
			return err
		}
		if err := syncRelocationDirectory(s.root, path.Dir(card.Path)); err != nil {
			return err
		}
		if err := bundleStep(step, fmt.Sprintf("after-card-%d", i)); err != nil {
			return err
		}
	}
	batch, err := intentdoc.Parse(record.Batch)
	if err != nil {
		return err
	}
	if _, err := contextDirectories(s.root, "batch", batch.ID(), true); err != nil {
		return err
	}
	path, err := contextPath("batch", batch.ID(), batch.Revision())
	if err != nil {
		return err
	}
	if err := publishBundleFile(s.root, path, record.Batch); err != nil {
		return err
	}
	if err := bundleStep(step, "after-batch"); err != nil {
		return err
	}
	// The bundle journal is an on-disk contract distinct from stdout; it
	// deliberately does not read the stdout vocabulary.
	s.journal.Records[index].Status = "completed"
	if err := saveBundles(s.root, s.journal, false); err != nil {
		return err
	}
	if err := bundleStep(step, "after-completed-receipt"); err != nil {
		return err
	}
	if err := s.shared.finishBundle(s.root, s.journal, s.journal.Records[index]); err != nil {
		return err
	}
	return bundleStep(step, "after-common-clear")
}
