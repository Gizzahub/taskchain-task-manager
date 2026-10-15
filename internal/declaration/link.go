package declaration

import (
	"reflect"

	"github.com/Gizzahub/taskchain-task-manager/internal/executiontiming"
)

const LinkSchemaV1 = "task-manager.declaration-link/v1"

// Link is a separate immutable event envelope referencing a declaration and
// optionally one legacy timing invocation. It does not extend evidence_v1 or
// assert writer provenance, execution authority, causation, or a completed join.
type Link struct {
	Schema        string      `json:"schema"`
	EventID       string      `json:"event_id"`
	OccurredAt    string      `json:"occurred_at"`
	Repository    Repository  `json:"repository"`
	Runtime       Runtime     `json:"runtime"`
	DeclarationID string      `json:"declaration_id"`
	Invocation    *Invocation `json:"invocation,omitempty"`
}

type Invocation struct {
	Schema string `json:"schema"`
	RunID  string `json:"run_id"`
}

func DecodeLink(data []byte) (Link, error) {
	var out Link
	if err := decodeWire(data, LinkSchemaV1, &out); err != nil {
		return Link{}, err
	}
	if err := out.Validate(); err != nil {
		return Link{}, err
	}
	return out, nil
}

func (l Link) Validate() error {
	if l.Schema != LinkSchemaV1 {
		return ErrUnsupported
	}
	if !text(l.DeclarationID, 256) || !text(l.EventID, 256) || !timestamp(l.OccurredAt) {
		return ErrInvalid
	}
	if err := validateIdentity(l.Repository, l.Runtime); err != nil {
		return err
	}
	if l.Invocation != nil {
		if l.Invocation.Schema != executiontiming.SchemaV1 {
			return ErrUnsupported
		}
		if !text(l.Invocation.RunID, 256) {
			return ErrInvalid
		}
	}
	return nil
}

// LinkRetransmission is a pure comparison. It never appends an audit event.
func LinkRetransmission(a, b Link) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	if err := b.Validate(); err != nil {
		return "", err
	}
	if a.EventID != b.EventID {
		return "distinct", nil
	}
	if reflect.DeepEqual(a, b) {
		return "duplicate", nil
	}
	return "conflict", nil
}
