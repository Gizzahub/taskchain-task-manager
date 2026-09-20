package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// completedOwnerRejoinBoard returns a board whose same-common rejoin
// transaction has completed, so protocol 6 is legitimately admitted.
func completedOwnerRejoinBoard(t *testing.T) ownerRejoinApplyFixture {
	t.Helper()
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 1}})
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatalf("baseline rejoined board is not admitted: %v", err)
	}
	return fx
}

func TestProtocol6RefusedWithoutIntactLocalEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		want   string
		mutate func(t *testing.T, fx ownerRejoinApplyFixture)
	}{
		{
			name: "receipt-absent",
			want: "completed owner-rejoin receipt",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				if err := os.Remove(filepath.Join(fx.target, ownerRejoinReceiptFile)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "receipt-back-to-pending",
			want: "still pending",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				planRaw, err := OwnerRejoinPlanBytes(fx.plan)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := OwnerRejoinReceiptBytes(ownerRejoinReceipt(fx.plan, bytesDigest(planRaw), "pending"))
				if err != nil {
					t.Fatal(err)
				}
				writeOwnerRejoinFixtureFile(t, filepath.Join(fx.target, ownerRejoinReceiptFile), raw, 0o600)
			},
		},
		{
			name: "plan-artifact-absent",
			want: "plan artifact that is missing",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				planRaw, err := OwnerRejoinPlanBytes(fx.plan)
				if err != nil {
					t.Fatal(err)
				}
				name, err := ownerRejoinPlanArtifactName(bytesDigest(planRaw))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(fx.target, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "payload-artifact-absent",
			want: "retained artifact",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				name, err := ownerRejoinPayloadArtifactName(fx.plan.PayloadSHA256)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(fx.target, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "capacity-artifact-absent",
			want: "retained artifact",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				if err := os.Remove(filepath.Join(fx.target, fx.plan.Artifacts[0].Path)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "transition-journal-third-bytes",
			want: "completed owner-rejoin target",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				name := filepath.Join(fx.target, transitionsFile)
				raw, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				writeOwnerRejoinFixtureFile(t, name, append(append([]byte(nil), raw...), ' '), 0o600)
			},
		},
		{
			name: "transition-journal-mode",
			want: "mode contradicts",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				if err := os.Chmod(filepath.Join(fx.target, transitionsFile), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "receipt-mode-widened",
			want: "mode or size invalid",
			mutate: func(t *testing.T, fx ownerRejoinApplyFixture) {
				if err := os.Chmod(filepath.Join(fx.target, ownerRejoinReceiptFile), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := completedOwnerRejoinBoard(t)
			tc.mutate(t, fx)
			_, err := Ready(fx.target)
			if err == nil {
				t.Fatal("protocol 6 admitted without intact local evidence")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

// A rejoin receipt from another board must never admit protocol 6 here.
func TestProtocol6RefusesForeignReceipt(t *testing.T) {
	t.Parallel()
	fx := completedOwnerRejoinBoard(t)
	other := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 1}})
	if err := applyOwnerRejoinSameCommon(other.target, other.plan, other.payload, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ownerRejoinReceiptFile} {
		raw, err := os.ReadFile(filepath.Join(other.target, name))
		if err != nil {
			t.Fatal(err)
		}
		writeOwnerRejoinFixtureFile(t, filepath.Join(fx.target, name), raw, 0o600)
	}
	otherPlanRaw, err := OwnerRejoinPlanBytes(other.plan)
	if err != nil {
		t.Fatal(err)
	}
	planName, err := ownerRejoinPlanArtifactName(bytesDigest(otherPlanRaw))
	if err != nil {
		t.Fatal(err)
	}
	writeOwnerRejoinFixtureFile(t, filepath.Join(fx.target, planName), otherPlanRaw, 0o600)
	if _, err := Ready(fx.target); err == nil || !strings.Contains(err.Error(), "another board") {
		t.Fatalf("foreign receipt admitted protocol 6: %v", err)
	}
}
