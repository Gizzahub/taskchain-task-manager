// Package integrationqueue owns durable, repository-independent integration
// request coordination state. It deliberately does not invoke Git or providers.
package integrationqueue

import "time"

type Owner struct {
	Actor string `json:"actor"`
	Host  string `json:"host"`
	Kind  string `json:"kind"`
}
type Generation struct {
	Task      string    `json:"task"`
	Type      string    `json:"type"`
	Purpose   string    `json:"purpose"`
	Source    string    `json:"source"`
	Branch    string    `json:"branch"`
	Worktree  string    `json:"worktree"`
	Owner     Owner     `json:"owner"`
	StartedAt time.Time `json:"startedAt"`
}
type Spec struct {
	ID                string     `json:"id"`
	IdempotencyKey    string     `json:"idempotencyKey"`
	RepositoryID      string     `json:"repositoryId"`
	RepositoryPath    string     `json:"repositoryPath"`
	CommonDir         string     `json:"commonDir"`
	ExpectedTaskSHA   string     `json:"expectedTaskSHA"`
	ExpectedSourceSHA string     `json:"expectedSourceSHA"`
	Source            string     `json:"source"`
	Target            string     `json:"target"`
	GateProfile       string     `json:"gateProfile"`
	Resource          string     `json:"resource"`
	Generation        Generation `json:"generation"`
	Owner             Owner      `json:"owner"`
}
type State string

const (
	Queued               State = "queued"
	Admitted             State = "admitted"
	RevalidationRequired State = "revalidation-required"
	Running              State = "running"
	Succeeded            State = "succeeded"
	Failed               State = "failed"
	Cancelled            State = "cancelled"
)

type Claim struct {
	Owner      Owner      `json:"owner"`
	Generation Generation `json:"generation"`
	ClaimedAt  time.Time  `json:"claimedAt"`
	PID        int        `json:"pid"`
	Host       string     `json:"host"`
}
type ClaimLiveness string

const (
	ClaimLive    ClaimLiveness = "live"
	ClaimDead    ClaimLiveness = "dead"
	ClaimUnknown ClaimLiveness = "unknown"
)

type Request struct {
	Spec      Spec      `json:"spec"`
	State     State     `json:"state"`
	Sequence  uint64    `json:"sequence"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Claim     *Claim    `json:"claim,omitempty"`
	LastError string    `json:"lastError,omitempty"`
}
type View struct {
	Request             Request `json:"request"`
	CoordinatorPosition *int    `json:"coordinatorPosition,omitempty"`
	WaitReason          string  `json:"waitReason,omitempty"`
	ProviderPosition    string  `json:"providerPosition"`
	ProviderWaitReason  string  `json:"providerWaitReason"`
}
