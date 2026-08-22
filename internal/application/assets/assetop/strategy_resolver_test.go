package assetop

import "testing"

func TestResolveExistingAssetStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy string
		evidence ExistingAssetEvidence
		wantSkip bool
		wantWhy  string
	}{
		{
			name:     "verify skips canonical Drive asset with hash",
			strategy: "verify",
			evidence: ExistingAssetEvidence{DriveFileID: "drive-1", LegacyFileMD5: "sha256-1"},
			wantSkip: true,
			wantWhy:  ExistingReasonVerifiedAsset,
		},
		{
			name:     "verify processes Drive row without hash",
			strategy: "verify",
			evidence: ExistingAssetEvidence{DriveFileID: "drive-1"},
		},
		{
			name:     "verify processes hash row without Drive evidence",
			strategy: "verify",
			evidence: ExistingAssetEvidence{LegacyFileMD5: "sha256-1"},
		},
		{
			name:     "skip accepts Drive file id",
			strategy: "skip",
			evidence: ExistingAssetEvidence{DriveFileID: "drive-1"},
			wantSkip: true,
			wantWhy:  ExistingReasonDrivePresent,
		},
		{
			name:     "skip accepts Drive link",
			strategy: "skip",
			evidence: ExistingAssetEvidence{DriveLink: "https://drive.google.com/file/d/drive-1/view"},
			wantSkip: true,
			wantWhy:  ExistingReasonDrivePresent,
		},
		{
			name:     "skip processes row without Drive evidence",
			strategy: "skip",
			evidence: ExistingAssetEvidence{LegacyFileMD5: "sha256-1"},
		},
		{
			name:     "replace always processes",
			strategy: "replace",
			evidence: ExistingAssetEvidence{DriveFileID: "drive-1", DriveLink: "drive-link", LegacyFileMD5: "sha256-1"},
		},
		{
			name:     "empty strategy defaults to verify",
			strategy: "",
			evidence: ExistingAssetEvidence{DriveFileID: "drive-1", LegacyFileMD5: "sha256-1"},
			wantSkip: true,
			wantWhy:  ExistingReasonVerifiedAsset,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveExistingAssetStrategy(tt.strategy, tt.evidence)
			if got.Skip != tt.wantSkip {
				t.Fatalf("Skip = %v, want %v (decision=%+v)", got.Skip, tt.wantSkip, got)
			}
			if got.Reason != tt.wantWhy {
				t.Fatalf("Reason = %q, want %q", got.Reason, tt.wantWhy)
			}
		})
	}
}
