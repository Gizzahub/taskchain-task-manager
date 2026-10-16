package declaration

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

const restartFixtureSchema = "task-manager.restart-fixture/v1"
const restartFixtureWriter = "synthetic-task-manager/v1"

// This envelope is a separate synthetic storage format, never declaration v1
// or a production authority token. Same-UID hostile writers are out of scope.
type restartFixtureEnvelope struct {
	Schema string `json:"schema"`
	Writer string `json:"writer"`
	Record Record `json:"record"`
}

func encodeRestartFixture(record Record) ([]byte, error) {
	b, err := json.Marshal(restartFixtureEnvelope{restartFixtureSchema, restartFixtureWriter, record})
	if err != nil || len(b) > MaxBytes {
		return nil, ErrInvalid
	}
	return b, nil
}

func decodeRestartFixture(data []byte) (Record, error) {
	var e restartFixtureEnvelope
	if err := decodeWire(data, restartFixtureSchema, &e); err != nil {
		return Record{}, err
	}
	if e.Writer != restartFixtureWriter || e.Record.Execution == nil || e.Record.Subject.Topic != nil || e.Record.Privacy.State != "omitted" {
		return Record{}, ErrInvalid
	}
	if err := e.Record.Validate(); err != nil {
		return Record{}, err
	}
	return e.Record, nil
}

// RestartFixtureWriter remains explicitly sandbox-only. It has no production
// activation, authority-selection, retention mutation, CLI or environment route.
// The caller selects a trusted synthetic collection; decoding/importing an
// envelope independently never constructs a trusted handle.
type RestartFixtureWriter struct{ writer *FixtureWriter }

func NewRestartFixtureWriter(sandbox string, runtime *taskruntime.Service) (*RestartFixtureWriter, error) {
	w, err := NewFixtureWriter(sandbox, runtime)
	if err != nil {
		return nil, err
	}
	w.restart = true
	return &RestartFixtureWriter{w}, nil
}

func (w *RestartFixtureWriter) Write(ctx context.Context, record Record, link Link, consume func(Link) error) (FixtureResult, error) {
	if w == nil || w.writer == nil {
		return FixtureResult{}, ErrUnavailable
	}
	return w.writer.Write(ctx, record, link, consume)
}

// Read restores historical synthetic writer provenance only from the explicitly
// selected owner-private namespace, not from a supplied JSON blob or live ledger.
// Repository is the caller's trusted selection, not inferred from stored data.
// It does not query execution state or grant current ownership. Sync is repeated
// before trust so visibility after an interrupted publish is not durability proof.
// Missing/legacy/unsupported/unsafe data stays unavailable; no file is created.
func (w *RestartFixtureWriter) Read(repository Repository, id string) (FixtureStored, error) {
	if w == nil || w.writer == nil || !text(id, 256) {
		return FixtureStored{}, ErrUnavailable
	}
	fwriter := w.writer
	root, err := fwriter.openCollection(repository.CommonDir, false)
	if err != nil {
		return FixtureStored{}, err
	}
	defer root.Close()
	name := fixtureName("declaration", id)
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || !ownerPrivate(before, 0o600) || before.Size() > MaxBytes {
		return FixtureStored{}, ErrUnavailable
	}
	f, err := root.OpenFile(name, readFlags(), 0)
	if err != nil {
		return FixtureStored{}, ErrUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !ownerPrivate(opened, 0o600) || !os.SameFile(before, opened) || opened.Size() > MaxBytes {
		return FixtureStored{}, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return FixtureStored{}, ErrUnavailable
	}
	record, err := decodeRestartFixture(data)
	if err != nil {
		return FixtureStored{}, err
	}
	if record.ID != id || !reflect.DeepEqual(record.Repository, repository) {
		return FixtureStored{}, ErrUnavailable
	}
	if fwriter.fail("restart-read-file-sync") != nil || f.Sync() != nil {
		return FixtureStored{}, ErrFixtureDurability
	}
	if err := fwriter.syncCollection(root, "restart-read"); err != nil {
		return FixtureStored{}, err
	}
	if err := fwriter.verifyCollection(repository.CommonDir, root); err != nil {
		return FixtureStored{}, err
	}
	return FixtureStored{record: record, trusted: true}, nil
}
