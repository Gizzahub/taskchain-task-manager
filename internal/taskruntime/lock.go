package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A live lock is never removed. Reclaim runs only from a mutating command,
// and only after the bytes still name a dead pid on this host.
const (
	reclaimGuardPoll = 10 * time.Millisecond
	reclaimGuardWait = 500 * time.Millisecond
)

var errMutationLockExists = errors.New("task runtime mutation lock exists")

func (s stateStore) lockPath() string { return filepath.Join(s.dir, "mutation.lock") }

func (s stateStore) guardPath() string { return s.lockPath() + ".reclaim" }

// lock creates mutation.lock. An existing lock is reclaimed only when its
// owner is a dead process on this host. Two reclaimers serialize on a
// short-lived guard and re-read the bytes before deleting, so the slower
// one cannot remove the lock the faster one just took.
func (s stateStore) lock() (func(), error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	path := s.lockPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil && os.IsExist(err) {
		if reclaimErr := s.reclaimDeadLock(); reclaimErr != nil {
			return nil, reclaimErr
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	}
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: %s", errMutationLockExists, path)
		}
		return nil, err
	}
	failed := func(writeErr error) (func(), error) {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, writeErr
	}
	if _, err := fmt.Fprintf(f, "pid=%d host=%s created=%s\n", os.Getpid(), lockHost(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return failed(fmt.Errorf("write mutation lock metadata: %w", err))
	}
	if err := f.Sync(); err != nil {
		return failed(fmt.Errorf("sync mutation lock: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close mutation lock: %w", err)
	}
	_ = syncDir(s.dir)
	return func() { _ = os.Remove(path) }, nil
}

func (s stateStore) reclaimDeadLock() error {
	path := s.lockPath()
	seen, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read mutation lock: %w", err)
	}
	info := s.parseLockInfo(seen, time.Now())
	if !info.Reclaimable {
		return fmt.Errorf("%w: %s", errMutationLockExists, path)
	}
	guardPath := s.guardPath()
	guard, err := os.OpenFile(guardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	for wait := time.Duration(0); err != nil && os.IsExist(err) && wait < reclaimGuardWait; wait += reclaimGuardPoll {
		time.Sleep(reclaimGuardPoll)
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return nil
		}
		guard, err = os.OpenFile(guardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	}
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("task runtime mutation lock reclaim is in progress or was interrupted; inspect and remove %s", guardPath)
		}
		return fmt.Errorf("create mutation lock reclaim guard: %w", err)
	}
	_ = guard.Close()
	defer func() { _ = os.Remove(guardPath) }()
	current, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reread mutation lock: %w", err)
	}
	if !bytes.Equal(current, seen) {
		return fmt.Errorf("%w: %s", errMutationLockExists, path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove dead-owner mutation lock: %w", err)
	}
	_ = syncDir(s.dir)
	return nil
}

// lockBlocks reports whether a reader should treat the ledger as mid-mutation.
// A dead-owner lock does not block, because the next mutation reclaims it.
// A lock that exists but cannot be read does block.
func (s stateStore) lockBlocks(now time.Time) bool {
	lock, err := s.lockInfo(now)
	if err != nil {
		return !os.IsNotExist(err)
	}
	return !lock.Reclaimable
}

func (s stateStore) lockInfo(now time.Time) (LockInfo, error) {
	b, err := os.ReadFile(s.lockPath())
	if err != nil {
		return LockInfo{}, err
	}
	return s.parseLockInfo(b, now), nil
}

func (s stateStore) parseLockInfo(b []byte, now time.Time) LockInfo {
	info := LockInfo{Path: s.lockPath()}
	for _, field := range strings.Fields(string(b)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			info.PID, _ = strconv.Atoi(value)
		case "host":
			info.Host = value
		case "created":
			info.CreatedAt, _ = time.Parse(time.RFC3339, value)
		}
	}
	if !info.CreatedAt.IsZero() {
		age := now.UTC().Sub(info.CreatedAt)
		if age > 0 {
			info.AgeSeconds = int64(age.Seconds())
		}
	}
	if info.PID > 0 {
		running := pidRunning(info.PID)
		info.PIDRunning = &running
		info.Reclaimable = !running && info.Host != "" && info.Host == lockHost()
	}
	return info
}

// pidRunning is replaceable in tests so a recorded pid can be dead or alive
// without depending on which pids the machine has free.
var pidRunning = func(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	signalErr := process.Signal(syscall.Signal(0))
	return signalErr == nil || errors.Is(signalErr, syscall.EPERM)
}

// lockHost is the OS hostname with whitespace replaced, because the lock
// file is a single line of space-separated fields. An unknown hostname is
// empty and therefore never reclaimable. It is not the task owner host.
func lockHost() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(host), "_")
}

// lockWait retries contention only; storage/reclaim failures remain visible.
func (s stateStore) lockWait(ctx context.Context, wait, poll time.Duration) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unlock, err := s.lock()
		if err == nil {
			if canceled := ctx.Err(); canceled != nil {
				unlock()
				return nil, canceled
			}
			return unlock, nil
		}
		if !errors.Is(err, errMutationLockExists) {
			return nil, err
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
