package boardruntime

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// ErrCommitted means the board directory exchange succeeded and a later step
// failed. Callers must not treat that as a reason to run the exchange again.
var ErrCommitted = errors.New("board maintenance committed")

// ErrUnsupported means this platform cannot exchange two directories atomically.
var ErrUnsupported = errors.New("atomic board exchange unsupported")

type planError struct{ Field, Reason string }

func (e *planError) Error() string { return fmt.Sprintf("maintenance plan %s: %s", e.Field, e.Reason) }

func invalidPlan(field, reason string) error { return &planError{field, reason} }

type MaintenanceRepository struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
}

type MaintenanceBinding struct {
	Identity string `json:"identity"`
	Version  string `json:"version"`
	Digest   string `json:"digest"`
	Path     string `json:"path"`
}

type MaintenanceFile struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Digest     string   `json:"digest"`
	References []string `json:"references"`
}

type MaintenanceMapping struct {
	From      string `json:"from"`
	To        string `json:"to"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
}

type MaintenanceCard struct {
	Identity     string                     `json:"identity"`
	Path         string                     `json:"path"`
	Scope        string                     `json:"scope"`
	Priority     string                     `json:"priority"`
	State        string                     `json:"state"`
	Parent       string                     `json:"parent"`
	Criteria     []string                   `json:"criteria"`
	Dependencies []string                   `json:"dependencies"`
	Evidence     []string                   `json:"evidence"`
	Unknown      map[string]json.RawMessage `json:"unknown"`
}

type MaintenancePlan struct {
	Version      int                   `json:"version"`
	Repository   MaintenanceRepository `json:"repository"`
	Board        string                `json:"board"`
	Controller   MaintenanceBinding    `json:"controller"`
	Policy       MaintenanceBinding    `json:"policy"`
	Rule         MaintenanceBinding    `json:"rule"`
	SourceSchema MaintenanceBinding    `json:"source_schema"`
	TargetSchema MaintenanceBinding    `json:"target_schema"`
	Before       []MaintenanceFile     `json:"before"`
	After        []MaintenanceFile     `json:"after"`
	Mappings     []MaintenanceMapping  `json:"mappings"`
	BeforeCards  []MaintenanceCard     `json:"before_cards"`
	AfterCards   []MaintenanceCard     `json:"after_cards"`
}

func planLabel(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func planRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func digestOK(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func safePlanPath(p string) bool {
	if p == "" || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || strings.Contains(p, "\\") || strings.ContainsAny(p, ":") || p == "." || p == ".." {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if strings.TrimSpace(segment) != segment || strings.HasSuffix(segment, ".") || strings.EqualFold(segment, ".git") {
			return false
		}
	}
	return !strings.ContainsFunc(p, unicode.IsControl)
}

func withinBoard(p, board string) bool { return strings.HasPrefix(p, board+"/") }
