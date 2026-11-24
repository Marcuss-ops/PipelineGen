package reconciliation

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type tableVerifier struct {
	result *scriptpkg.VerifiedLocation
}

func (v tableVerifier) Verify(_ context.Context, _, _, _ string) (*scriptpkg.VerifiedLocation, error) {
	return v.result, nil
}

func TestVerifyAndReconcileStateTable(t *testing.T) {
	const (
		oldLink       = "https://drive.google.com/file/d/OLD_ID/view"
		canonicalLink = "https://drive.google.com/file/d/CANONICAL_ID/view"
		assetID       = "asset-1"
		sceneID       = "scene-0"
	)
	cases := []struct {
		name          string
		state         scriptpkg.LocationState
		link          string
		verifiedLink  string
		wantLink      string
		wantWarning   string
		wantOK        int
		wantUpdated   int
		wantMissing   int
		wantTrashed   int
		wantInaccess  int
		wantMalformed int
	}{
		{name: "verified unchanged", state: scriptpkg.LocationStateVerified, link: canonicalLink, verifiedLink: canonicalLink, wantLink: canonicalLink, wantOK: 1},
		{name: "verified rewritten", state: scriptpkg.LocationStateVerified, link: oldLink, verifiedLink: canonicalLink, wantLink: canonicalLink, wantUpdated: 1},
		{name: "updated", state: scriptpkg.LocationStateUpdated, link: oldLink, verifiedLink: canonicalLink, wantLink: canonicalLink, wantUpdated: 1},
		{name: "missing", state: scriptpkg.LocationStateMissing, link: oldLink, wantLink: "", wantWarning: "MISSING", wantMissing: 1},
		{name: "trashed", state: scriptpkg.LocationStateTrashed, link: oldLink, wantLink: "", wantWarning: "TRASHED", wantTrashed: 1},
		{name: "inaccessible", state: scriptpkg.LocationStateInaccessible, link: oldLink, wantLink: "", wantWarning: "INACCESSIBLE", wantInaccess: 1},
		{name: "malformed", state: scriptpkg.LocationStateMalformed, link: oldLink, wantLink: "", wantWarning: "MALFORMED", wantMalformed: 1},
		{name: "orphan", state: scriptpkg.LocationStateOrphanDriveFile, link: oldLink, wantLink: "", wantWarning: "ORPHAN", wantMissing: 1},
		{name: "broken location", state: scriptpkg.LocationStateBrokenAssetLocation, link: oldLink, wantLink: "", wantWarning: "BROKEN", wantMissing: 1},
		{name: "duplicate", state: scriptpkg.LocationStateDuplicate, link: oldLink, wantLink: "", wantWarning: "DUPLICATE", wantMalformed: 1},
		{name: "unknown state", state: scriptpkg.LocationState("corrupt"), link: oldLink, wantLink: "", wantWarning: "UNKNOWN", wantMalformed: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			linkPtr := tc.link
			proc := NewAssetLocationReconciliationProcessor(tableVerifier{result: &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: "FILE_ID",
				DriveLink:   tc.verifiedLink,
				State:       tc.state,
			}})
			got, err := proc.verifyAndReconcile(context.Background(), assetID, "FILE_ID", tc.link, &linkPtr, "clip", sceneID)
			if err != nil {
				t.Fatalf("verifyAndReconcile: %v", err)
			}
			if linkPtr != tc.wantLink {
				t.Fatalf("link = %q, want %q", linkPtr, tc.wantLink)
			}
			if got.ok != tc.wantOK || got.updated != tc.wantUpdated || got.missing != tc.wantMissing ||
				got.trashed != tc.wantTrashed || got.inaccessible != tc.wantInaccess || got.malformed != tc.wantMalformed {
				t.Fatalf("counters = ok:%d updated:%d missing:%d trashed:%d inaccessible:%d malformed:%d",
					got.ok, got.updated, got.missing, got.trashed, got.inaccessible, got.malformed)
			}
			if tc.wantWarning != "" && !strings.Contains(got.warning, tc.wantWarning) {
				t.Fatalf("warning = %q, want substring %q", got.warning, tc.wantWarning)
			}
			if tc.wantWarning == "" && got.warning != "" {
				t.Fatalf("unexpected warning: %q", got.warning)
			}
			wantDurable := tc.state != scriptpkg.LocationStateVerified || tc.link != tc.verifiedLink
			if got.durableMutation != wantDurable {
				t.Fatalf("durableMutation = %t, want %t", got.durableMutation, wantDurable)
			}
			if wantDurable && got.assetChange == nil {
				t.Fatal("durable outcome must emit an asset location change")
			}
			if !wantDurable && got.assetChange != nil {
				t.Fatalf("non-durable outcome emitted asset change: %#v", got.assetChange)
			}
		})
	}
}

func TestVerifyAndReconcileRejectsIncompleteCanonicalMetadata(t *testing.T) {
	link := "https://drive.google.com/file/d/OLD/view"
	proc := NewAssetLocationReconciliationProcessor(tableVerifier{result: &scriptpkg.VerifiedLocation{
		State: scriptpkg.LocationStateVerified,
	}})
	got, err := proc.verifyAndReconcile(context.Background(), "asset-1", "OLD", link, &link, "clip", "scene-0")
	if err != nil {
		t.Fatalf("verifyAndReconcile: %v", err)
	}
	if link != "" {
		t.Fatalf("incomplete canonical metadata must clear the link, got %q", link)
	}
	if !got.durableMutation || got.assetChange == nil || !strings.Contains(got.warning, "incomplete canonical Drive metadata") {
		t.Fatalf("unexpected incomplete-metadata outcome: %#v", got)
	}
}
