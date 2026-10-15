//go:build !unix

package declaration

import "os"

// No equivalent owner/ACL proof is implemented on this platform. Fail closed
// without creating state or changing existing permissions.
func ownerPrivate(os.FileInfo, os.FileMode) bool { return false }
func readFlags() int                             { return os.O_RDONLY }
