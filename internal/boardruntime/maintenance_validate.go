package boardruntime

import (
	"path"
	"reflect"
	"slices"
	"strings"
)

func validatePlan(plan *MaintenancePlan) error {
	if plan.Version != 1 {
		return invalidPlan("version", "must be 1")
	}
	if !planLabel(plan.Repository.ID) || !planLabel(plan.Repository.Branch) || !planRevision(plan.Repository.Base) {
		return invalidPlan("repository", "id, branch, and base are required")
	}
	if !safePlanPath(plan.Board) {
		return invalidPlan("board", "must be a safe relative path")
	}
	for _, pair := range []struct {
		name  string
		value MaintenanceBinding
	}{
		{"controller", plan.Controller}, {"policy", plan.Policy}, {"rule", plan.Rule},
		{"source_schema", plan.SourceSchema}, {"target_schema", plan.TargetSchema},
	} {
		if !planLabel(pair.value.Identity) || !planLabel(pair.value.Version) || !digestOK(pair.value.Digest) {
			return invalidPlan(pair.name, "identity, version, and sha256 digest are required")
		}
	}
	before, err := planFiles(plan.Before, "before")
	if err != nil {
		return err
	}
	after, err := planFiles(plan.After, "after")
	if err != nil {
		return err
	}
	if err := planRefs(before, "before"); err != nil || planRefs(after, "after") != nil {
		if err == nil {
			err = planRefs(after, "after")
		}
		return err
	}
	if err := preserveClosure(plan.Board, before, after); err != nil {
		return err
	}
	for _, binding := range []MaintenanceBinding{plan.Policy, plan.Rule, plan.SourceSchema, plan.TargetSchema} {
		if !safePlanPath(binding.Path) || before[binding.Path].Kind != "reference" || before[binding.Path].Digest != binding.Digest {
			return invalidPlan("binding", "authority content must bind an immutable reference file")
		}
	}
	if plan.Controller.Path != "" && (before[plan.Controller.Path].Kind != "reference" || before[plan.Controller.Path].Digest != plan.Controller.Digest) {
		return invalidPlan("controller", "declared controller file is not bound in closure")
	}
	moves, err := planMappings(plan.Mappings, before, after)
	if err != nil {
		return err
	}
	if err := preserveRefs(before, after, moves); err != nil {
		return err
	}
	if err := planCards(plan.BeforeCards, before, "before_cards", plan.Board); err != nil {
		return err
	}
	if err := planCards(plan.AfterCards, after, "after_cards", plan.Board); err != nil {
		return err
	}
	return compareCards(plan.BeforeCards, plan.AfterCards, moves)
}

func planFiles(files []MaintenanceFile, field string) (map[string]MaintenanceFile, error) {
	if len(files) == 0 {
		return nil, invalidPlan(field, "must not be empty")
	}
	out := map[string]MaintenanceFile{}
	portable := map[string]bool{}
	for _, file := range files {
		if !safePlanPath(file.Path) || !digestOK(file.Digest) || (file.Kind != "card" && file.Kind != "reference" && file.Kind != "evidence") {
			return nil, invalidPlan(field, "contains invalid path or digest")
		}
		if portable[strings.ToLower(file.Path)] {
			return nil, invalidPlan(field, "duplicate path")
		}
		portable[strings.ToLower(file.Path)] = true
		out[file.Path] = file
	}
	for name := range portable {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if portable[parent] {
				return nil, invalidPlan(field, "file/directory path collision")
			}
		}
	}
	return out, nil
}

func planRefs(files map[string]MaintenanceFile, field string) error {
	for _, file := range files {
		seen := map[string]bool{}
		for _, ref := range file.References {
			if !safePlanPath(ref) || seen[ref] || files[ref].Path == "" {
				return invalidPlan(field, "dangling or duplicate reference")
			}
			seen[ref] = true
		}
	}
	return nil
}

func preserveClosure(board string, before, after map[string]MaintenanceFile) error {
	for _, files := range []map[string]MaintenanceFile{before, after} {
		for _, file := range files {
			if file.Path == board {
				return invalidPlan("board", "board root cannot be a file")
			}
			if file.Kind == "card" && !withinBoard(file.Path, board) {
				return invalidPlan("files", "card escapes board")
			}
		}
	}
	for name, file := range before {
		if file.Kind != "card" && !fileEqual(file, after[name]) {
			return invalidPlan("closure", "non-card authority/reference/evidence changed")
		}
	}
	return nil
}

func fileEqual(a, b MaintenanceFile) bool {
	return a.Path == b.Path && a.Kind == b.Kind && a.Digest == b.Digest && slices.Equal(a.References, b.References)
}

func preserveRefs(before, after map[string]MaintenanceFile, moves map[string]MaintenanceMapping) error {
	for name, file := range before {
		target := name
		if move, ok := moves[name]; ok {
			target = move.To
		}
		refs := make([]string, 0, len(file.References))
		for _, ref := range file.References {
			if move, ok := moves[ref]; ok {
				ref = move.To
			}
			refs = append(refs, ref)
		}
		slices.Sort(refs)
		actual := append([]string(nil), after[target].References...)
		slices.Sort(actual)
		if !slices.Equal(refs, actual) {
			return invalidPlan("references", "reference closure changed")
		}
	}
	return nil
}

func planMappings(mappings []MaintenanceMapping, before, after map[string]MaintenanceFile) (map[string]MaintenanceMapping, error) {
	if len(before) != len(after) {
		return nil, invalidPlan("files", "added or deleted history")
	}
	out := map[string]MaintenanceMapping{}
	to := map[string]bool{}
	for _, mapping := range mappings {
		if !safePlanPath(mapping.From) || !safePlanPath(mapping.To) || mapping.From == mapping.To || mapping.FromState == "" || mapping.FromState != mapping.ToState || before[mapping.From].Kind != "card" || after[mapping.To].Kind != "card" || to[mapping.To] || out[mapping.From].From != "" {
			return nil, invalidPlan("mappings", "invalid, duplicate, or dangling mapping")
		}
		out[mapping.From] = mapping
		to[mapping.To] = true
	}
	resolved := map[string]bool{}
	for name := range before {
		target := name
		if mapping, ok := out[name]; ok {
			target = mapping.To
		}
		if after[target].Path == "" || resolved[target] || after[target].Kind != before[name].Kind {
			return nil, invalidPlan("files", "non-bijective mapping or file kind change")
		}
		resolved[target] = true
	}
	for name := range after {
		if !resolved[name] {
			return nil, invalidPlan("files", "added history")
		}
	}
	return out, nil
}

func planCards(cards []MaintenanceCard, files map[string]MaintenanceFile, field, board string) error {
	ids := map[string]bool{}
	paths := map[string]bool{}
	for _, card := range cards {
		if !planLabel(card.Identity) || !planLabel(card.State) || !planLabel(card.Priority) || !withinBoard(card.Path, board) || files[card.Path].Kind != "card" || ids[card.Identity] || paths[card.Path] {
			return invalidPlan(field, "invalid or duplicate identity/path")
		}
		ids[card.Identity] = true
		paths[card.Path] = true
		seen := map[string]bool{}
		for _, dep := range card.Dependencies {
			if dep == "" || seen[dep] {
				return invalidPlan(field, "invalid dependency")
			}
			seen[dep] = true
		}
	}
	for _, file := range files {
		if file.Kind == "card" && !paths[file.Path] {
			return invalidPlan(field, "card file lacks semantic snapshot")
		}
	}
	for _, card := range cards {
		for _, ref := range append(append([]string{}, card.Dependencies...), card.Parent) {
			if ref != "" && !ids[ref] {
				return invalidPlan(field, "dangling graph reference")
			}
		}
	}
	return nil
}

func compareCards(before, after []MaintenanceCard, moves map[string]MaintenanceMapping) error {
	left := map[string]MaintenanceCard{}
	right := map[string]MaintenanceCard{}
	for _, card := range before {
		left[card.Identity] = card
	}
	for _, card := range after {
		right[card.Identity] = card
	}
	if len(left) != len(right) {
		return invalidPlan("cards", "added or deleted semantic history")
	}
	for id, from := range left {
		to, ok := right[id]
		if !ok {
			return invalidPlan("cards", "added or deleted semantic history")
		}
		expected := from.Path
		if move, mapped := moves[from.Path]; mapped {
			expected = move.To
			if from.State != to.State || from.State != move.FromState || from.State != move.ToState {
				return invalidPlan("mappings", "state equivalence mismatch")
			}
		} else if from.State != to.State {
			return invalidPlan("cards", "state changed without mapping")
		}
		if to.Path != expected {
			return invalidPlan("cards", "path changed without exact mapping")
		}
		from.Path, from.State = "", ""
		to.Path, to.State = "", ""
		if !reflect.DeepEqual(from, to) {
			return invalidPlan("cards", "semantic drift")
		}
	}
	return nil
}
