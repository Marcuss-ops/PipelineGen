// Package stockpipeline — orchestrator_fingerprint.go (Stock P1 split, July 2026).
//
// This file owns the artifact ID helper functions extracted from
// orchestrator_steps.go: ChunkArtifactID, ChunkArtifactFilename,
// MetadataArtifactID. These are pure functions that derive stable
// identifiers from the run fingerprint.
//
// godlike/07 no-fake-availability: same fingerprint ⇒ same ArtifactID
// across retries.
package stockpipeline

import "strconv"

// ChunkArtifactID returns the canonical logical ArtifactID for a
// single chunk. Stable across retries of the same logical run
// (same fingerprint) — Drive FileID is LOCATION (changes per retry
// if the DriveUpload re-runs), but the logical IDENTITY (this
// string) stays constant per godlike/07 no-fake-availability.
//
// Format: stock:<run_fingerprint>:chunk:<chunk_index>
func ChunkArtifactID(runFingerprint string, chunkIndex int) string {
	return "stock:" + runFingerprint + ":chunk:" + strconv.Itoa(chunkIndex)
}

// ChunkArtifactFilename returns the canonical filename for a
// single chunk. Truncates the fingerprint to the first 12 hex
// chars for readable on-disk filenames while preserving enough
// entropy for human auditing. Full fingerprint remains in the
// ArtifactID where it matters for byte-equality comparisons.
func ChunkArtifactFilename(runFingerprint string, chunkIndex int) string {
	fpShort := runFingerprint
	if len(fpShort) > 12 {
		fpShort = fpShort[:12]
	}
	return "stock_" + fpShort + "_chunk_" + strconv.Itoa(chunkIndex) + ".mp4"
}

// MetadataArtifactID returns the canonical logical ArtifactID for
// the per-run metadata.json. Format: stock:<run_fingerprint>:metadata
//
// Same fingerprint ⇒ same ArtifactID across retries
// (godlike/07 no-fake-availability invariant).
func MetadataArtifactID(runFingerprint string) string {
	return "stock:" + runFingerprint + ":metadata"
}
