package taskstore

import (
	"bytes"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
)

const sharedPhaseActiveWire = `{"schemaVersion":1,"namespaceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","boardPath":"tasks","phase":"active","reserved":[],"participants":[{"root":"/work/a","head":"0000000000000000000000000000000000000000","snapshot":"1111111111111111111111111111111111111111111111111111111111111111","originalLedger":"2222222222222222222222222222222222222222222222222222222222222222","targetLedger":"3333333333333333333333333333333333333333333333333333333333333333"}]}`

const sharedPhaseInitializingWire = `{"schemaVersion":1,"namespaceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","boardPath":"tasks","phase":"initializing","reserved":[],"participants":[{"root":"/work/a","head":"0000000000000000000000000000000000000000","snapshot":"1111111111111111111111111111111111111111111111111111111111111111","originalLedger":"2222222222222222222222222222222222222222222222222222222222222222","targetLedger":"3333333333333333333333333333333333333333333333333333333333333333"}]}`

const sharedPhaseUnknownWire = `{"schemaVersion":1,"namespaceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","boardPath":"tasks","phase":"unknown","reserved":[],"participants":[{"root":"/work/a","head":"0000000000000000000000000000000000000000","snapshot":"1111111111111111111111111111111111111111111111111111111111111111","originalLedger":"2222222222222222222222222222222222222222222222222222222222222222","targetLedger":"3333333333333333333333333333333333333333333333333333333333333333"}]}`

func TestSharedPhaseOutputMapsKnownSchema1Literals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "active",
			raw:  sharedPhaseActiveWire,
			want: "{\"outputVersion\":1,\"namespaceId\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"phase\":\"active\",\"worktrees\":1,\"reservedCount\":0}\n",
		},
		{
			name: "initializing",
			raw:  sharedPhaseInitializingWire,
			want: "{\"outputVersion\":1,\"namespaceId\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"phase\":\"initializing\",\"worktrees\":1,\"reservedCount\":0}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state, err := decodeSharedState([]byte(tc.raw))
			if err != nil {
				t.Fatalf("decodeSharedState: %v", err)
			}
			result, err := sharedResult(state)
			if err != nil {
				t.Fatalf("sharedResult: %v", err)
			}
			var out bytes.Buffer
			if err := outputformat.Encode(&out, result); err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if out.String() != tc.want {
				t.Fatalf("envelope=%q want=%q", out.String(), tc.want)
			}
		})
	}
}

func TestSharedPhaseOutputRejectsUnknownLikeReader(t *testing.T) {
	t.Parallel()
	_, readerErr := decodeSharedState([]byte(sharedPhaseUnknownWire))
	if readerErr == nil || readerErr.Error() != "invalid shared state header" {
		t.Fatalf("reader err=%v", readerErr)
	}
	state, err := decodeSharedState([]byte(sharedPhaseActiveWire))
	if err != nil {
		t.Fatalf("decodeSharedState: %v", err)
	}
	state.Phase = "unknown"
	_, mapErr := sharedResult(state)
	if mapErr == nil || mapErr.Error() != readerErr.Error() {
		t.Fatalf("sharedResult err=%v reader err=%v", mapErr, readerErr)
	}
}
