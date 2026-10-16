//go:build !unix

package taskruntime

import (
	"errors"
	"os"
)

var ErrFixtureBoundary = errors.New("synthetic fixture owner boundary unsupported")

func fixtureOpenFlags(flags int) int { return flags }

func OpenFixtureSandbox(string) (*os.Root, error) { return nil, ErrFixtureBoundary }
func fixtureSafeDirectory(os.FileInfo) bool       { return false }
