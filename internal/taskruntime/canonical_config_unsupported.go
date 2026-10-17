//go:build !darwin || !cgo

package taskruntime

import "fmt"

func readCanonicalConfig(string, canonicalReadHook) ([]byte, error) {
	return nil, fmt.Errorf("%w: native descriptor ACL support requires Darwin with cgo", errCanonicalUnavailable)
}
