package boardpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

type canonicalRoot struct {
	SchemaVersion int                 `json:"schema-version"`
	BoardPolicy   canonicalBlock      `json:"board-policy"`
	TaskBoard     *canonicalTaskBoard `json:"task-board,omitempty"`
}
type canonicalRootV5 struct {
	SchemaVersion int                 `json:"schema-version"`
	BoardPolicy   canonicalBlockV5    `json:"board-policy"`
	TaskBoard     *canonicalTaskBoard `json:"task-board,omitempty"`
}

type canonicalFileRoles struct {
	Enabled     bool                      `json:"enabled"`
	Projections []canonicalProjectionRole `json:"projections"`
	Guidance    []string                  `json:"guidance"`
	Templates   []string                  `json:"templates"`
}

type canonicalProjectionRole struct {
	Path     string `json:"path"`
	Owner    string `json:"owner"`
	Revision string `json:"revision"`
	Marker   string `json:"marker"`
}
type canonicalFileRolesV5 struct {
	Enabled     bool                        `json:"enabled"`
	Projections []canonicalProjectionRoleV5 `json:"projections"`
	Guidance    []canonicalContentRoleV5    `json:"guidance"`
	Templates   []canonicalContentRoleV5    `json:"templates"`
}
type canonicalProjectionRoleV5 struct {
	Path           string `json:"path"`
	Owner          string `json:"owner"`
	SourceRevision string `json:"source-revision"`
	Format         string `json:"format"`
}
type canonicalContentRoleV5 struct {
	Path           string `json:"path"`
	Owner          string `json:"owner"`
	SourceRevision string `json:"source-revision"`
	ContentSHA256  string `json:"content-sha256"`
}

type canonicalTaskBoard struct {
	ZoneSkills *map[string]string `json:"zone-skills,omitempty"`
}
type canonicalTransition struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}
type canonicalBlock struct {
	Zones       []string               `json:"zones"`
	ZoneStatus  map[string]string      `json:"zone-status"`
	Transitions []canonicalTransition  `json:"transitions"`
	Relocations *[]canonicalTransition `json:"relocations,omitempty"`
	KindStatus  *map[string]string     `json:"kind-status,omitempty"`
	Modules     *[]string              `json:"modules,omitempty"`
	FileRoles   *canonicalFileRoles    `json:"file-roles,omitempty"`
}
type canonicalBlockV5 struct {
	Zones       []string               `json:"zones"`
	ZoneStatus  map[string]string      `json:"zone-status"`
	Transitions []canonicalTransition  `json:"transitions"`
	Relocations *[]canonicalTransition `json:"relocations,omitempty"`
	KindStatus  *map[string]string     `json:"kind-status,omitempty"`
	Modules     *[]string              `json:"modules,omitempty"`
	FileRoles   *canonicalFileRolesV5  `json:"file-roles"`
}

func (p Policy) Canonical() ([]byte, error) {
	if p.workflow == nil || p.edges == nil || p.known == nil {
		return nil, errors.New("zero board policy has no canonical form")
	}
	zones := make([]string, 0, len(p.parked))
	for zone := range p.parked {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	status := map[string]string{}
	for _, zone := range zones {
		if value := p.parked[zone]; value != "" {
			status[zone] = value
		}
	}
	froms := make([]string, 0, len(p.edges))
	for from := range p.edges {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	transitions := make([]canonicalTransition, 0, len(froms))
	for _, from := range froms {
		targets := make([]string, 0, len(p.edges[from]))
		for to := range p.edges[from] {
			targets = append(targets, to)
		}
		sort.Strings(targets)
		transitions = append(transitions, canonicalTransition{From: from, To: targets})
	}
	version := p.schemaVersion
	if version == 0 {
		version = 1
	}
	block := canonicalBlock{Zones: zones, ZoneStatus: status, Transitions: transitions}
	if version >= 2 {
		froms = froms[:0]
		for from := range p.relocations {
			froms = append(froms, from)
		}
		sort.Strings(froms)
		rows := []canonicalTransition{}
		for _, from := range froms {
			targets := make([]string, 0, len(p.relocations[from]))
			for to := range p.relocations[from] {
				targets = append(targets, to)
			}
			sort.Strings(targets)
			rows = append(rows, canonicalTransition{From: from, To: targets})
		}
		block.Relocations = &rows
		kindStatus := map[string]string{}
		for kind, value := range p.kindStatus {
			kindStatus[kind] = value
		}
		block.KindStatus = &kindStatus
	}
	if version >= 3 {
		modules := append([]string(nil), p.modules...)
		if modules == nil {
			modules = []string{}
		}
		sort.Strings(modules)
		block.Modules = &modules
	}
	taskBoard := (*canonicalTaskBoard)(nil)
	if p.taskBoardDeclared {
		taskBoard = &canonicalTaskBoard{}
		if p.zoneSkills != nil {
			skills := make(map[string]string, len(p.zoneSkills))
			for zone, skill := range p.zoneSkills {
				skills[zone] = skill
			}
			taskBoard.ZoneSkills = &skills
		}
	}
	if version == 4 {
		roles := p.FileRoles()
		fileRoles := &canonicalFileRoles{Enabled: true, Projections: []canonicalProjectionRole{}, Guidance: []string{}, Templates: []string{}}
		for _, role := range roles {
			switch role.Kind {
			case FileRoleProjection:
				fileRoles.Projections = append(fileRoles.Projections, canonicalProjectionRole{Path: role.Path, Owner: role.Owner, Revision: role.Revision, Marker: role.Marker})
			case FileRoleGuidance:
				fileRoles.Guidance = append(fileRoles.Guidance, role.Path)
			case FileRoleTemplate:
				fileRoles.Templates = append(fileRoles.Templates, role.Path)
			}
		}
		block.FileRoles = fileRoles
	}
	if version == 5 {
		roles := p.FileRolesV5()
		fileRoles := &canonicalFileRolesV5{Enabled: true, Projections: []canonicalProjectionRoleV5{}, Guidance: []canonicalContentRoleV5{}, Templates: []canonicalContentRoleV5{}}
		for _, role := range roles {
			switch role.Kind {
			case FileRoleProjection:
				fileRoles.Projections = append(fileRoles.Projections, canonicalProjectionRoleV5{role.Path, role.Owner, role.SourceRevision, role.Format})
			case FileRoleGuidance:
				fileRoles.Guidance = append(fileRoles.Guidance, canonicalContentRoleV5{role.Path, role.Owner, role.SourceRevision, role.ContentSHA256})
			case FileRoleTemplate:
				fileRoles.Templates = append(fileRoles.Templates, canonicalContentRoleV5{role.Path, role.Owner, role.SourceRevision, role.ContentSHA256})
			}
		}
		root := canonicalRootV5{SchemaVersion: version, BoardPolicy: canonicalBlockV5{Zones: block.Zones, ZoneStatus: block.ZoneStatus, Transitions: block.Transitions, Relocations: block.Relocations, KindStatus: block.KindStatus, Modules: block.Modules, FileRoles: fileRoles}, TaskBoard: taskBoard}
		raw, err := json.Marshal(root)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxPolicyBytes {
			return nil, errors.New("canonical board-policy exceeds 64 KiB")
		}
		return raw, nil
	}
	root := canonicalRoot{SchemaVersion: version, BoardPolicy: block, TaskBoard: taskBoard}
	root.BoardPolicy = block
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPolicyBytes {
		return nil, errors.New("canonical board-policy exceeds 64 KiB")
	}
	return raw, nil
}

func (p Policy) Digest() (string, error) {
	raw, err := p.Canonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
