// Package finalization — types_drive_layout.go.
//
// Canonical Drive layout facts for generated-media artifacts. These are
// single canonical owners: a folder segment that is decided in more than one
// place silently diverges the moment one copy changes, and the divergence is
// only observable as artifacts landing in the wrong Drive folder.
package finalization

const (
	// OverlayChildFolder is the deterministic child folder below the
	// operator-configured overlay parent that receives every rendered overlay
	// video and its timing receipt.
	//
	// It is a Drive FOLDER SEGMENT. It is deliberately NOT the same fact as:
	//
	//   - the artifact source key "overlay" (see `artifact.Source` and
	//     `mapArtifactToDestination`, which switch on source == "overlay" or
	//     "chronon" to pick the DestinationScript tree). A source key names
	//     WHERE artifact bytes came from; this constant names WHERE they go.
	//     They are equal today only by historical accident, and merging them
	//     would make a future source rename silently relocate published files.
	//   - the filename fallback in `safeName`, which substitutes the literal
	//     "overlay" when a job ID is empty. That is a display-safe name, not a
	//     folder.
	//
	// Producers set this explicitly on VerifiedArtifact.DriveSubpath. The Drive
	// publisher additionally falls back to it for legacy pre-chronon producers
	// whose artifacts carry source == "overlay" and no explicit subpath.
	OverlayChildFolder = "overlay"
)
