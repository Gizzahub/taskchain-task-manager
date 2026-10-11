package boardruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
	"gopkg.in/yaml.v3"
)

const policyFile = "ce-tasks.yaml"

// TestingAfterExchange runs after a successful directory exchange. A non-nil
// error is returned with ErrCommitted. Tests set it; commands do not.
var TestingAfterExchange func() error

// MaintenanceRequest is one preview or apply.
type MaintenanceRequest struct {
	PlanFile string
	Apply    bool
	FilesDir string
}

// MaintenanceResult is the text and JSON report. Preview fills the counts only.
type MaintenanceResult struct {
	Committed   bool   `json:"committed"`
	PlanDigest  string `json:"planDigest"`
	Mappings    int    `json:"mappings"`
	Cards       int    `json:"cards"`
	Stage       string `json:"stage,omitempty"`
	ReceiptPath string `json:"receiptPath,omitempty"`
}

type maintenancePolicy struct {
	SchemaVersion int    `yaml:"schema-version,omitempty"`
	PolicyPath    string `yaml:"policy-path"`
}

// Maintain verifies a frozen plan under the mutation lock. Apply exchanges
// the board directory only after the lock, policy digest, and HEAD are
// rechecked. An error after that exchange wraps ErrCommitted.
func Maintain(ctx context.Context, root string, req MaintenanceRequest) (MaintenanceResult, error) {
	if strings.TrimSpace(req.PlanFile) == "" {
		return MaintenanceResult{}, fmt.Errorf("maintenance plan file is required")
	}
	svc := taskruntime.NewService(root, taskruntime.NewExecutor())
	var result MaintenanceResult
	err := svc.ProtectLock(ctx, func() error {
		if err := svc.RejectUnresolved(ctx); err != nil {
			return err
		}
		raw, err := os.ReadFile(req.PlanFile)
		if err != nil {
			return fmt.Errorf("read maintenance plan %s: %w", req.PlanFile, err)
		}
		plan, digest, err := DecodePlan(raw)
		if err != nil {
			return fmt.Errorf("decode maintenance plan: %w", err)
		}
		policy, err := loadPolicy(root)
		if err != nil {
			return fmt.Errorf("load task-maintenance policy: %w", err)
		}
		if policy == nil {
			return fmt.Errorf("task-maintenance policy is not declared in %s", policyFile)
		}
		if filepath.Clean(policy.PolicyPath) != filepath.Clean(plan.Policy.Path) {
			return fmt.Errorf("policy path mismatch: configured %s != plan %s", policy.PolicyPath, plan.Policy.Path)
		}
		policyBytes, err := os.ReadFile(filepath.Join(root, policy.PolicyPath))
		if err != nil {
			return fmt.Errorf("read authority policy %s: %w", policy.PolicyPath, err)
		}
		policyDigest := fmt.Sprintf("%x", sha256.Sum256(policyBytes))
		if policyDigest != plan.Policy.Digest {
			return fmt.Errorf("policy authority revoked or altered: configured %s digest %s != plan %s", policy.PolicyPath, policyDigest, plan.Policy.Digest)
		}
		head, err := revParse(ctx, root, "HEAD")
		if err != nil {
			return fmt.Errorf("resolve git HEAD: %w", err)
		}
		if head != plan.Repository.Base {
			return fmt.Errorf("repository base revision stale: head %s != plan base %s", head, plan.Repository.Base)
		}
		for _, file := range plan.Before {
			if file.Kind != "reference" {
				continue
			}
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
			if err != nil {
				return fmt.Errorf("reference file %s missing: %w", file.Path, err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(body)) != file.Digest {
				return fmt.Errorf("reference file %s digest mismatch", file.Path)
			}
		}
		result = MaintenanceResult{PlanDigest: digest, Mappings: len(plan.Mappings), Cards: len(plan.AfterCards)}
		if !req.Apply {
			return nil
		}
		files, err := replacementCards(root, req.FilesDir, plan)
		if err != nil {
			return err
		}
		receiptObj := map[string]any{
			"plan": digest, "appliedAt": time.Now().UTC().Format(time.RFC3339),
			"controller": plan.Controller.Identity, "policy": plan.Policy.Identity, "rule": plan.Rule.Version,
		}
		receipt, err := json.MarshalIndent(receiptObj, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal receipt: %w", err)
		}
		receipt = append(receipt, '\n')
		lockPath, err := svc.LockPath(ctx)
		if err != nil {
			return err
		}
		exchanged, err := exchangeBoard(exchangeRequest{
			Root: root, Plan: plan, Files: files, Receipt: receipt,
			Precondition: func() error {
				if _, statErr := os.Stat(lockPath); statErr != nil {
					return fmt.Errorf("mutation lock lost before exchange: %w", statErr)
				}
				live, readErr := os.ReadFile(filepath.Join(root, policy.PolicyPath))
				if readErr != nil {
					return fmt.Errorf("policy file missing before exchange: %w", readErr)
				}
				if fmt.Sprintf("%x", sha256.Sum256(live)) != policyDigest {
					return fmt.Errorf("policy file modified before exchange")
				}
				again, headErr := revParse(ctx, root, "HEAD")
				if headErr != nil || again != plan.Repository.Base {
					return fmt.Errorf("git HEAD drifted before exchange")
				}
				return nil
			},
		})
		result.Stage = exchanged.Stage
		result.ReceiptPath = filepath.ToSlash(filepath.Join(plan.Board, receiptName))
		result.Committed = exchanged.Committed
		return err
	})
	return result, err
}

func replacementCards(root, filesDir string, plan MaintenancePlan) (map[string][]byte, error) {
	from := map[string]string{}
	for _, mapping := range plan.Mappings {
		from[mapping.To] = mapping.From
	}
	out := map[string][]byte{}
	for _, file := range plan.After {
		if file.Kind != "card" {
			continue
		}
		var data []byte
		if filesDir != "" {
			body, err := os.ReadFile(filepath.Join(filesDir, filepath.Base(file.Path)))
			if err == nil {
				data = body
			}
		}
		if data == nil {
			source := file.Path
			if mapped, ok := from[file.Path]; ok {
				source = mapped
			}
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
			if err != nil {
				return nil, fmt.Errorf("read card replacement %s (from %s): %w", file.Path, source, err)
			}
			data = body
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != file.Digest {
			return nil, fmt.Errorf("replacement card %s digest mismatch: expected %s", file.Path, file.Digest)
		}
		out[file.Path] = data
	}
	return out, nil
}

func loadPolicy(root string) (*maintenancePolicy, error) {
	raw, err := os.ReadFile(filepath.Join(root, policyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", policyFile, err)
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("decode %s: %w", policyFile, err)
	}
	node, ok := top["task-maintenance"]
	if !ok {
		return nil, nil
	}
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		return nil, err
	}
	var policy maintenancePolicy
	dec := yaml.NewDecoder(bytes.NewReader(encoded))
	dec.KnownFields(true)
	if err := dec.Decode(&policy); err != nil {
		return nil, fmt.Errorf("decode %s task-maintenance: %w", policyFile, err)
	}
	if strings.TrimSpace(policy.PolicyPath) == "" {
		return nil, fmt.Errorf("%s task-maintenance: policy-path is required", policyFile)
	}
	return &policy, nil
}
