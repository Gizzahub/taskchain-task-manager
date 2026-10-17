//go:build !darwin || !cgo

package taskruntime

import "testing"

func TestCanonicalAuthorityUnsupported(t *testing.T) {
	data, err := readCanonicalConfig("/synthetic", nil)
	if err == nil || data != nil {
		t.Fatal("unsupported platform accepted protected read")
	}
}
