package boardruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// TestingBeforeExchange runs after the stage is built and just before the
// caller precondition. Tests use it to mutate the live board in the window
// between plan validation and the directory exchange.
var TestingBeforeExchange func()

// verifyLiveBefore re-reads every file the plan lists under `before` and
// refuses when its live digest no longer matches. It runs immediately before
// the directory exchange so an edit made after validation cannot be swapped
// away unnoticed.
func verifyLiveBefore(root string, before []MaintenanceFile) error {
	for _, file := range before {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return fmt.Errorf("before file %s unreadable before exchange: %w", file.Path, err)
		}
		if live := fmt.Sprintf("%x", sha256.Sum256(body)); live != file.Digest {
			return fmt.Errorf("before file %s changed before exchange: live digest %s != plan %s", file.Path, live, file.Digest)
		}
	}
	return nil
}
