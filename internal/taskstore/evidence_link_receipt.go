package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validEventID(s string) bool {
	return s != "" && len(s) <= 512 && !strings.ContainsAny(s, "\x00/\\")
}
func validReason(s string) bool {
	switch s {
	case "provider_policy", "model_refusal", "approval_denied", "user_excluded", "environment_blocked", "check_failed", "normal_stop", "unknown":
		return true
	}
	return false
}
func validCertainty(s string) bool { return s == "confirmed" || s == "suspected" || s == "unknown" }
func validIdentitySource(s string) bool {
	return s == "host_configuration" || s == "runtime_reported" || s == "unknown"
}
func stringValue(v any) string { s, _ := v.(string); return s }
func number(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		return 0
	}
	i, err := n.Int64()
	if err != nil {
		return 0
	}
	return int(i)
}
func exactKeys(m map[string]any, keys ...string) bool {
	return len(m) == len(keys) && hasKeys(m, keys...)
}
func allowedKeys(m map[string]any, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return false
		}
	}
	return true
}
func hasKeys(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func parseEvidenceReceipt(raw []byte, taskID, digest, ref string) (evidenceLinkRecord, error) {
	var v map[string]any
	if err := strictDecode(raw, &v); err != nil {
		return evidenceLinkRecord{}, fmt.Errorf("parse interruption receipt: %w", err)
	}
	if !exactKeys(v, "schema_version", "contract", "event", "classification") || number(v["schema_version"]) != 1 || stringValue(v["contract"]) != "agent-interruption-v1" {
		return evidenceLinkRecord{}, errors.New("unsupported interruption receipt contract")
	}
	event, ok := v["event"].(map[string]any)
	if !ok || !allowedKeys(event, "id", "runtime", "hook", "session_id", "turn_id", "task_id", "provider", "model", "original_payload_sha256", "payload", "received_at", "working_dir") || !hasKeys(event, "id", "runtime", "hook", "original_payload_sha256", "payload", "received_at") {
		return evidenceLinkRecord{}, errors.New("event has unknown or missing fields")
	}
	classification, ok := v["classification"].(map[string]any)
	if !ok || !allowedKeys(classification, "event_id", "reason", "certainty", "evidence", "provider", "model", "error", "started_at", "finished_at", "identity_source") || !hasKeys(classification, "event_id", "reason", "certainty", "provider", "model") {
		return evidenceLinkRecord{}, errors.New("classification has unknown or missing fields")
	}
	eventID := stringValue(event["id"])
	if !validEventID(eventID) || eventID != stringValue(classification["event_id"]) {
		return evidenceLinkRecord{}, errors.New("event and classification IDs must match")
	}
	if err := validateEvidenceEvent(event, taskID); err != nil {
		return evidenceLinkRecord{}, err
	}
	reason, certainty := stringValue(classification["reason"]), stringValue(classification["certainty"])
	if !validReason(reason) || !validCertainty(certainty) {
		return evidenceLinkRecord{}, errors.New("classification reason or certainty is invalid")
	}
	if reason == "unknown" && certainty != "unknown" {
		return evidenceLinkRecord{}, errors.New("unknown reason requires unknown certainty")
	}
	if certainty == "confirmed" {
		payload := event["payload"].(map[string]any)
		hostError, _ := payload["error"].(map[string]any)
		code := stringValue(hostError["code"])
		if !(reason == "provider_policy" && code == "cyber_policy" || reason == "approval_denied" && code == "PermissionDenied") {
			return evidenceLinkRecord{}, errors.New("confirmed classification requires matching structured observation")
		}
	}
	if stringValue(classification["provider"]) == "" || stringValue(classification["model"]) == "" {
		return evidenceLinkRecord{}, errors.New("classification provider and model must be non-empty strings")
	}
	if err := validateEvidenceClassification(classification); err != nil {
		return evidenceLinkRecord{}, err
	}
	source := stringValue(classification["identity_source"])
	if source == "" {
		source = "unknown"
	}
	if !validIdentitySource(source) {
		return evidenceLinkRecord{}, errors.New("classification identity source is invalid")
	}
	return evidenceLinkRecord{"", eventID, digest, ref, reason, certainty, stringValue(classification["provider"]), stringValue(classification["model"]), source}, nil
}

func validateEvidenceEvent(event map[string]any, taskID string) error {
	if stringValue(event["runtime"]) == "" || stringValue(event["hook"]) == "" || !validSHA256(stringValue(event["original_payload_sha256"])) {
		return errors.New("event identity or required fields are invalid")
	}
	if _, ok := event["payload"].(map[string]any); !ok {
		return errors.New("event payload must be an object")
	}
	if _, err := time.Parse(time.RFC3339, stringValue(event["received_at"])); err != nil {
		return errors.New("event received_at must be RFC3339")
	}
	for _, key := range []string{"session_id", "turn_id", "task_id", "provider", "model", "working_dir"} {
		if value, present := event[key]; present {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("event %s must be a string", key)
			}
		}
	}
	if claimed := stringValue(event["task_id"]); claimed != "" {
		id, err := cardid.Parse(claimed)
		expected, _ := cardid.Parse(taskID)
		if err != nil || id.Prefix != "TASK" || id.Key() != expected.Key() {
			return errors.New("receipt event task ID does not match requested task")
		}
	}
	return nil
}
func validateEvidenceClassification(m map[string]any) error {
	if value, present := m["evidence"]; present {
		items, ok := value.([]any)
		if !ok {
			return errors.New("classification evidence must be an array")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return errors.New("classification evidence items must be strings")
			}
		}
	}
	for _, key := range []string{"error", "started_at", "finished_at", "identity_source"} {
		if value, present := m[key]; present {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("classification %s must be a string", key)
			}
		}
	}
	for _, key := range []string{"started_at", "finished_at"} {
		if value := stringValue(m[key]); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("classification %s must be RFC3339", key)
			}
		}
	}
	return nil
}

// strictDecode rejects duplicate keys, trailing bytes and unknown fields when
// decoding into a struct. Its map form supports the receipt's nullable fields.
func strictDecode(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := strictJSONValue(dec, 0)
	if err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dec = json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
func strictJSONValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("JSON depth exceeds 16")
	}
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("null is not allowed")
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch d {
	case '{':
		m := map[string]any{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("object key must be a string")
			}
			if _, ok := m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON field %q", key)
			}
			x, err := strictJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = x
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return m, nil
	case '[':
		a := []any{}
		for dec.More() {
			if len(a) == 128 {
				return nil, errors.New("array exceeds 128 items")
			}
			x, err := strictJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, x)
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return a, nil
	}
	return nil, errors.New("unexpected JSON delimiter")
}
