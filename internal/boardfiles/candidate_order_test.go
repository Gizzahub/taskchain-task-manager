package boardfiles

import (
	"reflect"
	"testing"
)

func TestCardsOnlyFindingsSortPathThenDetailByUTF8Bytes(t *testing.T) {
	findings := []SnapshotFinding{
		{Path: "한글", Detail: "a"},
		{Path: "a", Detail: "한글"},
		{Path: "a", Detail: "z"},
		{Path: "Z", Detail: "z"},
		{Path: "a", Detail: "Z"},
	}
	sortSnapshotFindings(findings)
	want := []SnapshotFinding{
		{Path: "Z", Detail: "z"},
		{Path: "a", Detail: "Z"},
		{Path: "a", Detail: "z"},
		{Path: "a", Detail: "한글"},
		{Path: "한글", Detail: "a"},
	}
	if !reflect.DeepEqual(findings, want) {
		t.Fatalf("wrong path/detail byte order: %+v", findings)
	}
}
