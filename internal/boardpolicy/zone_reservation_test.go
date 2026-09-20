package boardpolicy

import (
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// A policy-free reader treats these names as zones wherever they appear in a
// path, which is only sound while no declared module can carry one. New()
// enforces that here; this pins the two lists together so neither can grow
// alone.
func TestStatusOpaqueZoneNamesCannotBeModules(t *testing.T) {
	for _, name := range []string{"archive", "_archive", "plan", "issue", "backlog"} {
		if !cardpath.IsStatusOpaqueZone(name) {
			t.Fatalf("%q lost its status-opaque marking", name)
		}
		if _, err := New(Declaration{Modules: []string{name}}); err == nil {
			t.Fatalf("module %q was accepted despite being a reserved zone", name)
		}
	}
	// The workflow aliases are the other half of the same reservation: they
	// decide a status, so a module must not be able to shadow one either.
	for _, name := range []string{"todo", "wip", "in_progress", "completed"} {
		if cardpath.WorkflowStatus(name) == "" {
			t.Fatalf("%q stopped resolving to a workflow status", name)
		}
		if _, err := New(Declaration{Modules: []string{name}}); err == nil {
			t.Fatalf("module %q was accepted despite naming a workflow zone", name)
		}
	}
}
