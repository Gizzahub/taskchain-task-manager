package card

import (
	"reflect"
	"testing"
)

func TestDependencyFieldTypes(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  []string
		bad   bool
	}{
		{"null", nil, false},
		{"[]", nil, false},
		{"TASK-1", []string{"TASK-1"}, false},
		{"[TASK-1, ' TASK-2 ']", []string{"TASK-1", "TASK-2"}, false},
		{"123", nil, true},
		{"true", nil, true},
		{"{id: TASK-1}", nil, true},
		{"[TASK-1, 123]", nil, true},
		{"[null]", nil, true},
		{"[[TASK-1]]", nil, true},
		{"['']", nil, true},
		{"' '", nil, true},
	} {
		t.Run(tt.value, func(t *testing.T) {
			doc, err := Parse([]byte("---\nid: TASK-2\ndepends-on: " + tt.value + "\n---\n"))
			if (err != nil) != tt.bad {
				t.Fatalf("error = %v, want invalid=%v", err, tt.bad)
			}
			if !tt.bad && !reflect.DeepEqual(doc.View().DependsOn, tt.want) {
				t.Fatalf("dependencies = %#v, want %#v", doc.View().DependsOn, tt.want)
			}
		})
	}
}
