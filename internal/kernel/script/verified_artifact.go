package script

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// VerifiedArtifact is the only artifact shape accepted by the common
// finalization path. Technical verification and rights policy are explicit.
type VerifiedArtifact struct {
	Candidate        SegmentAssetCandidate
	LocalPath        string
	MIMEType         string
	SizeBytes        int64
	ContentSHA256    string
	DurationMs       int64
	Width            int
	Height           int
	RightsStatus     string
	VerificationNote string
	Manifest         *job.ArtifactManifest
	// OutputDriveFolderID is the resolved Drive root selected by the
	// generation plan. Entity images are published below this root so a
	// script run is self-contained in Drive; the canonical entity catalog
	// remains the owner of reuse/indexing metadata.
	OutputDriveFolderID string
	// OutputDriveSubpath is the deterministic child path below
	// OutputDriveFolderID (for example: title/language/images).
	OutputDriveSubpath []string
}
