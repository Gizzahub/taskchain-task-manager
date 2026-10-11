package taskruntime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type stateStore struct{ dir string }

func newStateStore(commonDir string) stateStore {
	return stateStore{dir: filepath.Join(commonDir, RuntimeStateDir, "task-runtime", "v1")}
}

func (s stateStore) init() error { return os.MkdirAll(s.dir, 0o755) }

func (s stateStore) executionsPath() string { return filepath.Join(s.dir, "executions.json") }

func (s stateStore) receiptsPath() string { return filepath.Join(s.dir, "receipts.jsonl") }

// load reads executions.json. A missing file is an empty ledger. A present
// file that cannot be decoded is an error, and this read never rewrites it.
func (s stateStore) load() ([]Execution, error) {
	b, err := os.ReadFile(s.executionsPath())
	if os.IsNotExist(err) {
		return []Execution{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task executions: %w", err)
	}
	var out []Execution
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode task executions: %w", err)
	}
	return out, nil
}

func (s stateStore) save(executions []Execution) error {
	if err := s.init(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(executions, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, fmt.Sprintf("executions.%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.executionsPath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = syncDir(s.dir)
	return nil
}

func (s stateStore) appendReceipt(receipt Receipt) error {
	if err := s.init(); err != nil {
		return err
	}
	f, err := os.OpenFile(s.receiptsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	stat, statErr := f.Stat()
	created := statErr == nil && stat.Size() == 0
	if err := json.NewEncoder(f).Encode(receipt); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if created {
		_ = syncDir(s.dir)
	}
	return nil
}

// latestReceipts keeps the last decodable record for each task. An empty
// line or a broken record fails the whole read; nothing is skipped.
func (s stateStore) latestReceipts() (map[string]Receipt, error) {
	f, err := os.Open(s.receiptsPath())
	if os.IsNotExist(err) {
		return map[string]Receipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseReceipts(f)
}

func parseReceipts(r io.Reader) (map[string]Receipt, error) {
	out := map[string]Receipt{}
	reader := bufio.NewReader(r)
	for line := 1; ; line++ {
		record, readErr := reader.ReadBytes('\n')
		if len(record) > 0 {
			record = bytes.TrimSuffix(record, []byte("\n"))
			if len(bytes.TrimSpace(record)) == 0 {
				return nil, fmt.Errorf("decode receipt line %d: empty receipt record", line)
			}
			var receipt Receipt
			if err := json.Unmarshal(record, &receipt); err != nil {
				return nil, fmt.Errorf("decode receipt line %d: %w", line, err)
			}
			out[receipt.Task] = receipt
		}
		if errors.Is(readErr, io.EOF) {
			return out, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("read receipt line %d: %w", line, readErr)
		}
	}
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
