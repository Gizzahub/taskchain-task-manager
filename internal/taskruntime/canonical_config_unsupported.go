//go:build !darwin || !cgo

package taskruntime

import "fmt"

import "os"

func readCanonicalConfig(string, canonicalReadHook) ([]byte, error) {
	return nil, fmt.Errorf("%w: native descriptor ACL support requires Darwin with cgo", errCanonicalUnavailable)
}

func withCanonicalConfigPath(string, canonicalReadHook, func(*os.File, *os.File, []byte) error) error {
	return fmt.Errorf("%w: native descriptor ACL support requires Darwin with cgo", errCanonicalUnavailable)
}

func recheckCanonicalConfigPath(string, *os.File, []byte) error {
	return fmt.Errorf("%w: native descriptor ACL support requires Darwin with cgo", errCanonicalUnavailable)
}
