package boardfiles

import "context"

const candidatePolicyFileLimit = 64 << 10

// ReadCandidatePolicy reads one externally supplied candidate policy without
// following any path link. The returned bytes are bounded and tied to the
// pathname observed both before and after the read.
func ReadCandidatePolicy(ctx context.Context, name string) ([]byte, error) {
	return readCandidatePolicyPlatform(ctx, name, nil)
}
