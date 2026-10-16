//go:build !unix

package taskruntime

import (
	"errors"
	"os"
)

var ErrFixtureBoundary = errors.New("synthetic fixture owner boundary unsupported")

func OpenFixtureSandbox(string) (*os.Root, error) { return nil, ErrFixtureBoundary }
func fixtureSafeDirectory(os.FileInfo) bool       { return false }
