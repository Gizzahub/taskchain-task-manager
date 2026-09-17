package intentdoc

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

var bundleRequestID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var bundleModuleName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func validateBundle(request BundleRequest) error {
	if request.SchemaVersion != 1 || request.Kind != "task-bundle" || !bundleRequestID.MatchString(request.RequestID) {
		return errors.New("invalid bundle schema, kind or request ID")
	}
	if err := ValidateIdentity("batch", request.Batch.ID, request.Batch.Revision); err != nil {
		return fmt.Errorf("batch: %w", err)
	}
	if err := validateRef(request.Batch.Intent); err != nil {
		return fmt.Errorf("batch intent: %w", err)
	}
	if err := text(request.Batch.Gap, 16<<10, true); err != nil {
		return fmt.Errorf("batch gap: %w", err)
	}
	if err := texts(request.Batch.Constraints); err != nil {
		return fmt.Errorf("batch constraints: %w", err)
	}
	if err := texts(request.Batch.AuthorizationRefs); err != nil {
		return fmt.Errorf("batch authorizationRefs: %w", err)
	}
	if len(request.Tasks) == 0 || len(request.Tasks) > 128 {
		return errors.New("tasks requires 1..128 drafts")
	}
	keys := map[string]int{}
	ids := map[string]int{}
	keyIDs := map[string]string{}
	for i, task := range request.Tasks {
		if !criterionKey.MatchString(task.Key) || keys[task.Key] != 0 {
			return fmt.Errorf("task %d has invalid or duplicate key", i)
		}
		keys[task.Key] = i + 1
		if task.ID != "" {
			id, err := cardid.Parse(task.ID)
			if len(task.ID) > 16<<10 || err != nil || id.Prefix != "TASK" {
				return fmt.Errorf("task %s has invalid TASK ID", task.Key)
			}
			if ids[id.Key()] != 0 {
				return fmt.Errorf("task %s has duplicate TASK identity", task.Key)
			}
			ids[id.Key()] = i + 1
			keyIDs[task.Key] = id.Key()
		}
		if err := text(task.Title, 256, false); err != nil {
			return fmt.Errorf("task %s title: %w", task.Key, err)
		}
		var module, category string
		if task.Module != nil {
			module = *task.Module
		}
		if task.Category != nil {
			category = *task.Category
		}
		if err := validateDraftScope(module, category); err != nil {
			return fmt.Errorf("task %s scope: %w", task.Key, err)
		}
		if len(task.DependsOn) > 128 {
			return fmt.Errorf("task %s dependencies exceed 128", task.Key)
		}
		if err := validateDraftTemplate(task.Template); err != nil {
			return fmt.Errorf("task %s template: %w", task.Key, err)
		}
	}
	for _, task := range request.Tasks {
		seenRefs := map[string]bool{}
		for _, dep := range task.DependsOn {
			hasID, hasKey := dep.TaskID != "", dep.Key != ""
			if hasID == hasKey {
				return fmt.Errorf("task %s dependency must contain exactly one reference", task.Key)
			}
			if hasKey {
				if keys[dep.Key] == 0 || dep.Key == task.Key {
					return fmt.Errorf("task %s has invalid dependency key %s", task.Key, dep.Key)
				}
				ref := "key:" + dep.Key
				if id := keyIDs[dep.Key]; id != "" {
					ref = "id:" + id
				}
				if seenRefs[ref] {
					return fmt.Errorf("task %s has duplicate dependency %s", task.Key, dep.Key)
				}
				seenRefs[ref] = true
				continue
			}
			id, err := cardid.Parse(dep.TaskID)
			if len(dep.TaskID) > 16<<10 || err != nil || id.Prefix != "TASK" {
				return fmt.Errorf("task %s has invalid or duplicate dependency TASK", task.Key)
			}
			if task.ID != "" {
				own, _ := cardid.Parse(task.ID)
				if own.Key() == id.Key() {
					return fmt.Errorf("task %s depends on itself", task.Key)
				}
			}
			ref := "id:" + id.Key()
			if seenRefs[ref] {
				return fmt.Errorf("task %s has duplicate dependency TASK", task.Key)
			}
			seenRefs[ref] = true
		}
	}
	return validateDraftCycles(request.Tasks)
}

func validateDraftScope(module, category string) error {
	if module == "" && category == "" {
		return nil
	}
	if module == "" {
		return errors.New("category requires module")
	}
	if len(module) > 255 || !bundleModuleName.MatchString(module) {
		return errors.New("module must be a lowercase board scope name")
	}
	if category == "" {
		return nil
	}
	if strings.HasPrefix(category, "/") || strings.HasSuffix(category, "/") || strings.Contains(category, "\\") {
		return errors.New("category must be a relative slash-separated path")
	}
	parts := strings.Split(category, "/")
	for _, part := range parts {
		if len(part) > 255 {
			return errors.New("category component exceeds 255 bytes")
		}
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || cardpath.IsExcludedDirectory(part) || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return errors.New("category contains an empty, traversal, or hidden segment")
		}
	}
	return nil
}

func validateDraftTemplate(template *DraftTemplate) error {
	if template == nil {
		return nil
	}
	if err := validateInlineConfig(template.ValidationConfig); err != nil {
		return err
	}
	if err := text(template.Type, 16<<10, false); err != nil {
		return fmt.Errorf("type: %w", err)
	}
	if err := text(template.Priority, 16<<10, false); err != nil {
		return fmt.Errorf("priority: %w", err)
	}
	if err := text(template.Summary, 16<<10, false); err != nil {
		return fmt.Errorf("summary: %w", err)
	}
	if len(template.Criteria) == 0 || len(template.Criteria) > 128 {
		return errors.New("criteria requires 1..128 items")
	}
	for _, criterion := range template.Criteria {
		if err := text(criterion, 16<<10, false); err != nil {
			return err
		}
	}
	return nil
}

func validateInlineConfig(value string) error {
	if value == "" || len(value) > 64<<10 || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return errors.New("inline config must be bounded nonempty UTF-8 text")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return errors.New("inline config contains a forbidden control character")
		}
	}
	return nil
}

func validateDraftCycles(tasks []TaskDraft) error {
	byKey := map[string]TaskDraft{}
	for _, task := range tasks {
		byKey[task.Key] = task
	}
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return errors.New("task bundle dependencies contain a cycle")
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		for _, dep := range byKey[key].DependsOn {
			if dep.Key != "" {
				if err := visit(dep.Key); err != nil {
					return err
				}
			}
		}
		state[key] = 2
		return nil
	}
	for key := range byKey {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
