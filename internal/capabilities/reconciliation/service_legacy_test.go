package reconciliation

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// Legacy payload-key classification and asymmetric repair counters.

func TestReconcile_LocatorLegacy_AsymmetricKeyCounters(t *testing.T) {
	cases := []struct {
		name           string
		assetID        string
		driveLink      string // empty -> key absent
		localPath      string // empty -> key absent
		wantDriveLinks int
		wantLocalPaths int
	}{
		{
			name:           "drive_link_only",
			assetID:        "asset_d_only",
			driveLink:      "https://drive.example/x",
			localPath:      "",
			wantDriveLinks: 1,
			wantLocalPaths: 0,
		},
		{
			name:           "local_path_only",
			assetID:        "asset_l_only",
			driveLink:      "",
			localPath:      "/local/dump/x.mp4",
			wantDriveLinks: 0,
			wantLocalPaths: 1,
		},
		{
			name:           "both_keys",
			assetID:        "asset_both",
			driveLink:      "https://drive.example/x",
			localPath:      "/local/dump/x.mp4",
			wantDriveLinks: 1,
			wantLocalPaths: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{
				"asset_id":               tc.assetID,
				"name":                   "x",
				"source":                 "youtube",
				"lifecycle_state":        "ACTIVE",
				"embedding_version_text": "2026-06-16-v1",
			}
			if tc.driveLink != "" {
				payload["drive_link"] = tc.driveLink
			}
			if tc.localPath != "" {
				payload["local_path"] = tc.localPath
			}

			mtr := &stubMetrics{}
			payloadStub := &stubPayload{}
			svc := fixtureService(t,
				versionCheckSchema(),
				&stubQdrant{pointsByID: map[string]pointWithID{
					tc.assetID: {ID: canonicalPointID(tc.assetID), Payload: payload},
				}},
				&stubSQLite{rows: []AssetSnapshot{
					{ID: tc.assetID, LifecycleState: "ACTIVE"},
				}},
				mtr,
				withOutbox(&stubOutbox{}),
				withPayload(payloadStub),
				withPointIDFor(canonicalPointID),
				withLog(zap.NewNop()),
			)
			_, err := svc.Reconcile(context.Background(), ReconcileOptions{Collection: "coll", DryRun: false})
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			// Sum per-key RecordLegacyKeyStripped calls (the stub
			// emits one call per key, NOT per DeletePayloadKeys call).
			gotDrive := 0
			gotLocal := 0
			for _, s := range mtr.legacyStrips {
				switch s.legacyKey {
				case "drive_link":
					gotDrive += s.n
				case "local_path":
					gotLocal += s.n
				}
			}
			if gotDrive != tc.wantDriveLinks {
				t.Fatalf("drive_link strips=%d, want %d (all=%+v)", gotDrive, tc.wantDriveLinks, mtr.legacyStrips)
			}
			if gotLocal != tc.wantLocalPaths {
				t.Fatalf("local_path strips=%d, want %d (all=%+v)", gotLocal, tc.wantLocalPaths, mtr.legacyStrips)
			}
		})
	}
}
