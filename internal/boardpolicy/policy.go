// Package boardpolicy contains the pure, file-format-independent board policy.
package boardpolicy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type Declaration struct {
	Zones       []string
	ZoneStatus  map[string]string
	Transitions []Transition
	Relocations []Transition
	KindStatus  map[string]string
	Modules     []string
}

type Transition struct {
	From string
	To   []string
}

type Policy struct {
	schemaVersion int
	workflow      map[string]string
	parked        map[string]string
	edges         map[string]map[string]bool
	relocations   map[string]map[string]bool
	kindStatus    map[string]string
	modules       []string
	known         []string
}

func Default() Policy {
	return Policy{schemaVersion: 1, workflow: defaultStatuses(), parked: map[string]string{}, edges: defaultEdges(), relocations: map[string]map[string]bool{}, kindStatus: map[string]string{}, known: baseDirs()}
}

func New(d Declaration) (Policy, error) {
	workflow := defaultStatuses()
	parked := map[string]string{}
	seenZones := map[string]bool{}
	for _, zone := range d.Zones {
		if !validParkedName(zone) {
			return Policy{}, fmt.Errorf("invalid parked zone %q", zone)
		}
		if seenZones[zone] {
			return Policy{}, fmt.Errorf("duplicate parked zone %q", zone)
		}
		seenZones[zone] = true
		parked[zone] = ""
	}
	keys := make([]string, 0, len(d.ZoneStatus))
	for key := range d.ZoneStatus {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, zone := range keys {
		status := d.ZoneStatus[zone]
		if !seenZones[zone] {
			return Policy{}, fmt.Errorf("status for undeclared parked zone %q", zone)
		}
		if !knownStatus(status) {
			return Policy{}, fmt.Errorf("unknown status %q for parked zone %q", status, zone)
		}
		parked[zone] = status
	}
	edges := defaultEdges()
	if len(d.Transitions) > 0 {
		edges = map[string]map[string]bool{}
		for i, row := range d.Transitions {
			_, workflowSource := workflow[row.From]
			if row.From == "" || (!workflowSource && !seenZones[row.From]) {
				return Policy{}, fmt.Errorf("transition %d has unknown source %q", i, row.From)
			}
			if _, exists := edges[row.From]; exists {
				return Policy{}, fmt.Errorf("duplicate transition source %q", row.From)
			}
			if len(row.To) == 0 {
				return Policy{}, fmt.Errorf("transition %q has no targets", row.From)
			}
			edges[row.From] = map[string]bool{}
			for _, target := range row.To {
				if _, ok := workflow[target]; !ok {
					return Policy{}, fmt.Errorf("transition %q targets non-workflow zone %q", row.From, target)
				}
				if target == row.From {
					return Policy{}, fmt.Errorf("transition %q targets itself", row.From)
				}
				if edges[row.From][target] {
					return Policy{}, fmt.Errorf("transition %q repeats target %q", row.From, target)
				}
				edges[row.From][target] = true
			}
		}
	}
	relocations := map[string]map[string]bool{}
	if d.Relocations != nil {
		for i, row := range d.Relocations {
			if row.From == "" || len(row.To) == 0 {
				return Policy{}, fmt.Errorf("relocation %d requires a source and target", i)
			}
			if _, ok := relocations[row.From]; ok {
				return Policy{}, fmt.Errorf("duplicate relocation source %q", row.From)
			}
			if !relocationSource(row.From, workflow, parked) {
				return Policy{}, fmt.Errorf("relocation %d has unknown source %q", i, row.From)
			}
			relocations[row.From] = map[string]bool{}
			for _, target := range row.To {
				if !relocationTarget(target, workflow) {
					return Policy{}, fmt.Errorf("relocation %q targets unsupported zone %q", row.From, target)
				}
				if target == row.From {
					return Policy{}, fmt.Errorf("relocation %q targets itself", row.From)
				}
				if relocations[row.From][target] {
					return Policy{}, fmt.Errorf("relocation %q repeats target %q", row.From, target)
				}
				if !isKindDir(row.From) && !isKindDir(target) {
					return Policy{}, fmt.Errorf("relocation %q to %q must involve a kind endpoint", row.From, target)
				}
				relocations[row.From][target] = true
			}
		}
	}
	kindStatus := map[string]string{}
	for kind, status := range d.KindStatus {
		if !isKindDir(kind) {
			return Policy{}, fmt.Errorf("unknown kind-status key %q", kind)
		}
		if !knownStatus(status) {
			return Policy{}, fmt.Errorf("unknown status %q for kind %q", status, kind)
		}
		kindStatus[kind] = status
	}
	var modules []string
	if d.Modules != nil {
		modules = make([]string, len(d.Modules))
		copy(modules, d.Modules)
		seenModules := map[string]bool{}
		for _, module := range modules {
			if !validModuleName(module) {
				return Policy{}, fmt.Errorf("invalid module name %q", module)
			}
			if seenModules[module] {
				return Policy{}, fmt.Errorf("duplicate module %q", module)
			}
			seenModules[module] = true
			if workflow[module] != "" || isZoneAlias(module) || isKindDir(module) || isReserved(module) || seenZones[module] {
				return Policy{}, fmt.Errorf("module %q conflicts with a board zone or reserved name", module)
			}
		}
	}
	known := baseDirs()
	custom := make([]string, 0, len(parked))
	for zone := range parked {
		custom = append(custom, zone)
	}
	sort.Strings(custom)
	known = append(known, custom...)
	version := 1
	if d.Relocations != nil || d.KindStatus != nil {
		version = 2
	}
	if d.Modules != nil {
		version = 3
	}
	return Policy{schemaVersion: version, workflow: workflow, parked: parked, edges: edges, relocations: relocations, kindStatus: kindStatus, modules: modules, known: known}, nil
}

func relocationSource(zone string, workflow map[string]string, parked map[string]string) bool {
	return workflow[zone] != "" || parkedHas(parked, zone) || isKindDir(zone)
}

func parkedHas(parked map[string]string, zone string) bool {
	_, ok := parked[zone]
	return ok
}

func relocationTarget(zone string, workflow map[string]string) bool {
	return workflow[zone] != "" || isKindDir(zone)
}

func baseDirs() []string {
	zones := outputvocab.AllZones()
	dirs := make([]string, len(zones))
	for i, z := range zones {
		dirs[i] = string(z)
	}
	return dirs
}

func defaultStatuses() map[string]string {
	return map[string]string{
		string(outputvocab.ZoneTodo):    string(outputvocab.StatusPending),
		string(outputvocab.ZoneDoing):   string(outputvocab.StatusInProgress),
		string(outputvocab.ZoneReview):  string(outputvocab.StatusReview),
		string(outputvocab.ZoneBlocked): string(outputvocab.StatusBlocked),
		string(outputvocab.ZoneDone):    string(outputvocab.StatusDone),
	}
}

func validParkedName(zone string) bool {
	if zone == "" || strings.ToLower(zone) != zone || strings.ContainsAny(zone, "/\\\x00") || zone[0] < 'a' || zone[0] > 'z' {
		return false
	}
	for _, c := range zone[1:] {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return !isZoneAlias(zone) && !isKindDir(zone) && !isReserved(zone)
}

func isZoneAlias(zone string) bool {
	switch zone {
	case "todo", "todos", "pending", "doing", "doings", "in-progress", "in_progress", "inprogress", "wip", "review", "reviews", "in-review", "in_review", "blocked", "blockeds", "suspended", "suspend", "on-hold", "on_hold", "waiting", "done", "dones", "completed", "complete", "finished":
		return true
	}
	return false
}
func isKindDir(zone string) bool { return zone == "plan" || zone == "issue" || zone == "backlog" }
func isReserved(zone string) bool {
	switch zone {
	case "archive", "_archive", "evidence", ".ce", "readme", "index", "template", "git":
		return true
	}
	return false
}

func knownStatus(status string) bool {
	switch outputvocab.Status(status) {
	case outputvocab.StatusPending, outputvocab.StatusInProgress, outputvocab.StatusReview,
		outputvocab.StatusBlocked, outputvocab.StatusDone, outputvocab.StatusCancelled:
		return true
	}
	return false
}
func defaultEdges() map[string]map[string]bool {
	return map[string]map[string]bool{"todo": {"doing": true}, "doing": {"todo": true, "review": true, "blocked": true}, "blocked": {"todo": true, "doing": true}, "review": {"doing": true, "done": true}, "done": {"todo": true}}
}

func (p Policy) Workflow(zone string) bool {
	if p.workflow == nil {
		return false
	}
	_, ok := p.workflow[zone]
	return ok
}
func (p Policy) Parked(zone string) bool {
	if p.parked == nil || p.Workflow(zone) {
		return false
	}
	_, ok := p.parked[zone]
	return ok
}
func (p Policy) Status(zone string) (string, bool) {
	if p.workflow == nil {
		return "", false
	}
	if status, ok := p.workflow[zone]; ok {
		return status, true
	}
	status, ok := p.parked[zone]
	return status, ok && status != ""
}
func (p Policy) Allows(from, to string) bool {
	return p.edges != nil && p.edges[from] != nil && p.edges[from][to]
}
func (p Policy) AllowsRelocation(from, to string) bool {
	return p.relocations != nil && p.relocations[from] != nil && p.relocations[from][to]
}
func (p Policy) KindStatus(zone string) (string, bool) {
	status, ok := p.kindStatus[zone]
	return status, ok
}
func (p Policy) IsKind(zone string) bool { return isKindDir(zone) }
func (p Policy) KnownDirs() []string     { return append([]string(nil), p.known...) }
func (p Policy) ReadyZone() string       { return "todo" }
func (p Policy) DoneZone() string        { return "done" }
func (p Policy) InitialZone() string     { return "todo" }
