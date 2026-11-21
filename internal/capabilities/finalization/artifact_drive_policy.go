// Package finalization — artifact_drive_policy.go is the ONE decision about
// whether an artifact must be copied to Drive.
//
// Why this is a policy and not a flag at each call site:
//
//   - Drive is the DELIVERY surface. An intermediate (an overlay item, a
//     render segment, a timing sidecar) is already immutable and
//     content-addressed in the object store, and the pipeline can consume it
//     from there. Publishing it a second time is latency and quota with no
//     reader (measured 2026-11-17..20: 1732 drive_publish calls, 0.60 h; the
//     same bytes had just been uploaded to the object store by the render).
//   - A scattered flag makes "which artifacts go to Drive?" unanswerable, and
//     two call sites eventually disagree.
//
// The default is deliberately the HISTORICAL behaviour (`all`): turning the
// optimisation on is an explicit deployment decision, and rolling back is
// setting PIPELINE_ARTIFACT_DRIVE_POLICY=all without a rebuild.
package finalization

import (
	"fmt"
	"strings"
)

// ArtifactRole separates what must be delivered from what the pipeline only
// needs to read once.
type ArtifactRole string

const (
	// RoleDeliverable is user-facing output: final video, audio master,
	// subtitles, manifest.
	RoleDeliverable ArtifactRole = "deliverable"
	// RoleIntermediate is pipeline-internal bytes that already live in the
	// content-addressed object store.
	RoleIntermediate ArtifactRole = "intermediate"
)

// MetadataArtifactRole is the artifact-metadata key a producer sets to mark
// an intermediate. Absent means deliverable on purpose: an unclassified
// artifact must keep the safe (Drive) behaviour.
const MetadataArtifactRole = "artifact_role"

// DrivePolicyMode selects which roles are published to Drive.
type DrivePolicyMode string

const (
	// DrivePolicyAll publishes every artifact (historical behaviour).
	DrivePolicyAll DrivePolicyMode = "all"
	// DrivePolicyDeliverables publishes only RoleDeliverable artifacts;
	// intermediates stay content-addressed in the object store.
	DrivePolicyDeliverables DrivePolicyMode = "deliverables"
)

// DrivePolicy is the single owner of "does this artifact need Drive?".
type DrivePolicy struct {
	Mode DrivePolicyMode
}

// DefaultDrivePolicy is the historical behaviour. Keeping it the zero value
// means an unconfigured ArtifactPreparation cannot silently start skipping
// uploads.
func DefaultDrivePolicy() DrivePolicy { return DrivePolicy{Mode: DrivePolicyAll} }

// ParseDrivePolicy validates a configured mode. An unknown mode is an error,
// never a fallback: a typo must not decide where deliverables live.
func ParseDrivePolicy(raw string) (DrivePolicy, error) {
	switch DrivePolicyMode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", DrivePolicyAll:
		return DefaultDrivePolicy(), nil
	case DrivePolicyDeliverables:
		return DrivePolicy{Mode: DrivePolicyDeliverables}, nil
	default:
		return DrivePolicy{}, fmt.Errorf("finalization: unknown artifact drive policy %q (want %q or %q)", raw, DrivePolicyAll, DrivePolicyDeliverables)
	}
}

// DrivePolicyFromEnv reads the switch. Unset is the historical mode.
func DrivePolicyFromEnv(getenv func(string) string) (DrivePolicy, error) {
	if getenv == nil {
		return DefaultDrivePolicy(), nil
	}
	return ParseDrivePolicy(getenv("PIPELINE_ARTIFACT_DRIVE_POLICY"))
}

// Role reports the artifact's role from its metadata. An unrecognised or
// missing role is a DELIVERABLE: dropping a required upload is worse than
// paying for one unnecessary one.
func Role(artifact VerifiedArtifact) ArtifactRole {
	raw, _ := artifact.ArtifactMetadata[MetadataArtifactRole].(string)
	if ArtifactRole(strings.ToLower(strings.TrimSpace(raw))) == RoleIntermediate {
		return RoleIntermediate
	}
	return RoleDeliverable
}

// RequiresDrive is the decision every publisher must ask.
func (p DrivePolicy) RequiresDrive(artifact VerifiedArtifact) bool {
	if p.Mode == DrivePolicyDeliverables && Role(artifact) == RoleIntermediate {
		return false
	}
	return true
}

// ObjectStoreLocation is the canonical location of an intermediate that was
// NOT copied to Drive: the content address IS the address. FileID carries it
// so a manifest reader always has a resolvable reference.
func ObjectStoreLocation(artifact VerifiedArtifact) AssetLocation {
	return AssetLocation{
		Provider: "objectstore",
		FileID:   artifact.SHA256,
		Checksum: "sha256:" + artifact.SHA256,
	}
}
