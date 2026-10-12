// Package executiontiming writes opt-in, attempt-scoped execution timing sidecars.
package executiontiming

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const SchemaV1 = "task-manager.execution-timing/v1"

// Schema is retained as the producer-facing name for the current schema.
const Schema = SchemaV1

const (
	StatusSuccess   = "success"
	StatusFailure   = "failure"
	StatusCancelled = "cancelled"
	StatusTimeout   = "timeout"
	StatusUnknown   = "unknown"
)

// Context is the allowlisted DVA execution_evidence_v1 namespace.
type Context struct {
	SessionID    string `json:"session_id,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
	ToolCallID   string `json:"tool_call_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	RepositoryID string `json:"repository_id,omitempty"`
	RelationType string `json:"relation_type,omitempty"`
	RetryOf      string `json:"retry_of,omitempty"`
}

// Record describes one command invocation attempt. Raw IDs never appear in paths.
type Record struct {
	Schema              string    `json:"schema"`
	RunID               string    `json:"run_id"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	Status              string    `json:"status"`
	ExitCode            *int      `json:"exit_code,omitempty"`
	DurationMonotonicNS *int64    `json:"duration_monotonic_ns,omitempty"`
	ClockDomainID       string    `json:"clock_domain_id,omitempty"`
	Context             Context   `json:"execution_evidence_v1,omitempty"`
}

var contextKeys = map[string]func(*Context, string){
	"session_id": func(c *Context, v string) { c.SessionID = v }, "turn_id": func(c *Context, v string) { c.TurnID = v },
	"tool_call_id": func(c *Context, v string) { c.ToolCallID = v }, "task_id": func(c *Context, v string) { c.TaskID = v },
	"repository_id": func(c *Context, v string) { c.RepositoryID = v }, "relation_type": func(c *Context, v string) { c.RelationType = v },
	"retry_of": func(c *Context, v string) { c.RetryOf = v },
}

// ParseContext accepts precisely the public allowlist. It rejects duplicate keys.
func ParseContext(data []byte) (Context, error) {
	var out Context
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	if !utf8.Valid(data) {
		return out, errors.New("execution timing context must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return out, errors.New("execution timing context must be an object")
	}
	seen := map[string]bool{}
	for d.More() {
		keyToken, err := d.Token()
		if err != nil {
			return out, fmt.Errorf("read context key: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return out, errors.New("execution timing context key is not a string")
		}
		set, ok := contextKeys[key]
		if !ok {
			return out, fmt.Errorf("unknown execution timing context key %q", key)
		}
		if seen[key] {
			return out, fmt.Errorf("duplicate execution timing context key %q", key)
		}
		seen[key] = true
		var value string
		if err := d.Decode(&value); err != nil {
			return out, fmt.Errorf("context %s must be a string: %w", key, err)
		}
		if err := validateID(key, value); err != nil {
			return out, err
		}
		set(&out, value)
	}
	if _, err := d.Token(); err != nil {
		return out, fmt.Errorf("close context object: %w", err)
	}
	if err := requireEOF(d); err != nil {
		return out, err
	}
	if err := validateContext(out); err != nil {
		return out, err
	}
	return out, nil
}

func requireEOF(d *json.Decoder) error {
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing execution timing context data")
	}
	return nil
}

func Validate(r Record) error {
	if r.Schema != SchemaV1 {
		return fmt.Errorf("unsupported execution timing schema %q", r.Schema)
	}
	if err := validateID("run_id", r.RunID); err != nil {
		return err
	}
	if r.StartedAt.IsZero() {
		return errors.New("started_at is required")
	}
	if r.FinishedAt.IsZero() {
		return errors.New("finished_at is required")
	}
	if r.StartedAt.Location() != time.UTC || r.FinishedAt.Location() != time.UTC {
		return errors.New("execution timing timestamps must be UTC")
	}
	if r.Status != StatusSuccess && r.Status != StatusFailure && r.Status != StatusCancelled && r.Status != StatusTimeout && r.Status != StatusUnknown {
		return fmt.Errorf("invalid execution timing status %q", r.Status)
	}
	if r.ExitCode != nil && (*r.ExitCode < 0 || *r.ExitCode > 255) {
		return errors.New("exit_code must be between 0 and 255")
	}
	if r.DurationMonotonicNS != nil {
		if *r.DurationMonotonicNS < 0 {
			return errors.New("duration_monotonic_ns cannot be negative")
		}
		if err := validateID("clock_domain_id", r.ClockDomainID); err != nil {
			return err
		}
	} else if r.ClockDomainID != "" {
		return errors.New("clock_domain_id requires duration_monotonic_ns")
	}
	return validateContext(r.Context)
}

func validateContext(c Context) error {
	for _, field := range []struct{ n, v string }{{"session_id", c.SessionID}, {"turn_id", c.TurnID}, {"tool_call_id", c.ToolCallID}, {"task_id", c.TaskID}, {"repository_id", c.RepositoryID}, {"relation_type", c.RelationType}, {"retry_of", c.RetryOf}} {
		if field.v != "" {
			if err := validateID(field.n, field.v); err != nil {
				return err
			}
		}
	}
	switch c.RelationType {
	case "", "launches", "polls", "relates_to", "delegates_to", "retries":
	default:
		return fmt.Errorf("invalid relation_type %q", c.RelationType)
	}
	if c.RelationType == "launches" || c.RelationType == "polls" || c.RelationType == "relates_to" || c.RelationType == "delegates_to" {
		if c.SessionID == "" || c.ToolCallID == "" {
			return errors.New("tool relation requires session_id and tool_call_id")
		}
	}
	if c.RelationType == "retries" && c.RetryOf == "" {
		return errors.New("retries relation requires retry_of")
	}
	return nil
}

func validateID(name, value string) error {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid %s", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid %s", name)
		}
	}
	return nil
}
