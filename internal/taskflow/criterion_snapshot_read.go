package taskflow

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// snapshotEntry is one tracked blob. link is the raw symlink target and is
// set only for mode 120000; the symlink itself is written after validation.
type snapshotEntry struct {
	mode string
	oid  string
	path string
	link string
}

func readSnapshotListing(r io.Reader) ([]snapshotEntry, error) {
	return readSnapshotListingBounds(bufio.NewReader(r), productionSnapshotBounds(), criterionTreeListingLimit, criterionTreeRecordLimit)
}

func readSnapshotListingBounds(reader *bufio.Reader, bounds snapshotBounds, outputLimit, recordLimit int64) ([]snapshotEntry, error) {
	entries := make([]snapshotEntry, 0)
	var outputBytes int64
	for {
		record, eof, err := readSnapshotNULRecord(reader, &outputBytes, outputLimit, recordLimit)
		if err != nil {
			return nil, fmt.Errorf("read criterion tree: %w", err)
		}
		if eof {
			return entries, nil
		}
		if len(entries) == bounds.entries {
			return nil, fmt.Errorf("criterion snapshot exceeds %d entries", bounds.entries)
		}
		entry, err := parseSnapshotEntry(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
}

func readSnapshotNULRecord(reader *bufio.Reader, outputBytes *int64, outputLimit, recordLimit int64) ([]byte, bool, error) {
	record := make([]byte, 0)
	for {
		b, err := reader.ReadByte()
		if err == io.EOF {
			if len(record) == 0 {
				return nil, true, nil
			}
			return nil, false, fmt.Errorf("unterminated ls-tree record")
		}
		if err != nil {
			return nil, false, err
		}
		*outputBytes++
		if *outputBytes > outputLimit {
			return nil, false, fmt.Errorf("tree listing exceeds %d MiB", outputLimit>>20)
		}
		if b == 0 {
			return record, false, nil
		}
		if int64(len(record)) == recordLimit {
			return nil, false, fmt.Errorf("tree entry exceeds %d KiB", recordLimit>>10)
		}
		record = append(record, b)
	}
}

func parseSnapshotEntry(record []byte) (snapshotEntry, error) {
	meta, name, found := bytes.Cut(record, []byte{'\t'})
	if !found {
		return snapshotEntry{}, fmt.Errorf("parse criterion tree entry: missing path separator")
	}
	fields := bytes.Fields(meta)
	if len(fields) != 3 {
		return snapshotEntry{}, fmt.Errorf("parse criterion tree entry %q: invalid metadata", string(meta))
	}
	mode, kind, oid := string(fields[0]), string(fields[1]), string(fields[2])
	if kind == "commit" || mode == "160000" {
		return snapshotEntry{}, fmt.Errorf("criterion snapshot does not support submodule %q", string(name))
	}
	if kind != "blob" || (mode != "100644" && mode != "100755" && mode != "120000") {
		return snapshotEntry{}, fmt.Errorf("criterion snapshot does not support tree entry %q (mode %s, type %s)", string(name), mode, kind)
	}
	if err := checkCriterionPath(string(name)); err != nil {
		return snapshotEntry{}, err
	}
	return snapshotEntry{mode: mode, oid: oid, path: string(name)}, nil
}

func checkCriterionPath(name string) error {
	if name == "" || path.IsAbs(name) || filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return fmt.Errorf("criterion snapshot has unsafe path %q", name)
	}
	if name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("criterion snapshot has unsafe path %q", name)
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("criterion snapshot has unsafe path %q", name)
		}
	}
	return nil
}

func writeSnapshotBlobs(ctx context.Context, repoRoot, dir string, entries []snapshotEntry) error {
	if len(entries) == 0 {
		return nil
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := snapshotGitCommand(child, repoRoot, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(snapshotObjectRequests(entries))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git cat-file --batch: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git cat-file --batch: %w", err)
	}
	readErr := readSnapshotObjects(child, stdout, dir, entries)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("git cat-file --batch: %w: %s", waitErr, msg)
		}
		return fmt.Errorf("git cat-file --batch: %w", waitErr)
	}
	return nil
}

func snapshotObjectRequests(entries []snapshotEntry) string {
	var requests strings.Builder
	for _, entry := range entries {
		requests.WriteString(entry.oid)
		requests.WriteByte('\n')
	}
	return requests.String()
}

func readSnapshotObjects(ctx context.Context, reader io.Reader, dir string, entries []snapshotEntry) error {
	return readSnapshotObjectsBounds(ctx, bufio.NewReader(reader), dir, entries, productionSnapshotBounds())
}

func readSnapshotObjectsBounds(ctx context.Context, reader *bufio.Reader, dir string, entries []snapshotEntry, bounds snapshotBounds) error {
	var total int64
	for index := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write criterion snapshot stopped: %w", err)
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("git cat-file --batch: read record %d: %w", index+1, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != entries[index].oid || fields[1] != "blob" {
			return fmt.Errorf("git cat-file --batch: unexpected record %q", strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return fmt.Errorf("criterion blob %q exceeds %d bytes", entries[index].path, bounds.blob)
		}
		next, err := accountBlob(bounds, total, size, entries[index].path)
		if err != nil {
			return err
		}
		total = next
		data := make([]byte, size)
		if size > 0 {
			if _, err := io.ReadFull(reader, data); err != nil {
				return fmt.Errorf("git cat-file --batch: read %q: %w", entries[index].path, err)
			}
		}
		ending, err := reader.ReadByte()
		if err != nil || ending != '\n' {
			return fmt.Errorf("git cat-file --batch: read terminator for %q", entries[index].path)
		}
		if entries[index].mode == "120000" {
			entries[index].link = string(data)
			continue
		}
		if err := writeSnapshotFile(dir, entries[index], data); err != nil {
			return err
		}
	}
	return nil
}

func accountBlob(bounds snapshotBounds, consumed, size int64, name string) (int64, error) {
	if size < 0 || size > bounds.blob {
		return consumed, fmt.Errorf("criterion blob %q exceeds %d bytes", name, bounds.blob)
	}
	if bounds.total < consumed || size > bounds.total-consumed {
		return consumed, fmt.Errorf("criterion snapshot blobs exceed %d bytes", bounds.total)
	}
	return consumed + size, nil
}

func writeSnapshotFile(dir string, entry snapshotEntry, data []byte) error {
	filename := filepath.Join(dir, filepath.FromSlash(entry.path))
	if !pathInside(dir, filename) {
		return fmt.Errorf("criterion snapshot has unsafe path %q", entry.path)
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return fmt.Errorf("create criterion snapshot directory for %q: %w", entry.path, err)
	}
	mode := os.FileMode(0o644)
	if entry.mode == "100755" {
		mode = 0o755
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create distinct criterion snapshot file %q: %w", entry.path, err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write criterion snapshot file %q: %w", entry.path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close criterion snapshot file %q: %w", entry.path, closeErr)
	}
	// OpenFile applies the process umask. Set the mode again so a tracked
	// executable stays executable.
	if err := os.Chmod(filename, mode); err != nil {
		return fmt.Errorf("set criterion snapshot mode for %q: %w", entry.path, err)
	}
	return nil
}
