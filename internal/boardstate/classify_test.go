package boardstate

import "testing"

func TestClassifyAcceptsOnlyTypedEngineState(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := map[string]Kind{
		".task-manager-context":                                    Directory,
		".task-manager.lock":                                       Directory,
		".task-manager-ids.json":                                   File,
		".task-manager-stage-0123456789abcdef0123456789abcdef":     File,
		".task-manager-archive-capacity-" + digest + ".bin":        File,
		".task-manager-archive-rebind-" + digest + ".bin":          File,
		".task-manager-owner-rejoin-capacity-" + digest + ".bin":   File,
		".task-manager-owner-rejoin-plan-" + digest + ".json":      File,
		".task-manager-owner-rejoin-payload-" + digest + ".bin":    File,
		".task-manager-stage-not-hex":                              Unknown,
		".task-manager-archive-capacity-" + digest + ".json":       Unknown,
		".task-manager-owner-rejoin-plan-" + digest[:63] + ".json": Unknown,
		".hidden-unrelated":                                        Unknown,
		".task-manager-context/anything":                           Unknown,
		"todo/.task-manager-ids.json":                              Unknown,
	}
	for path, want := range cases {
		if got := Classify(path); got != want {
			t.Errorf("Classify(%q) = %v, want %v", path, got, want)
		}
	}
}
