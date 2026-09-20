// Package assets — clip_writer_helpers.go consolidates the helper
// functions previously split across clip_atomic_writer_asset.go,
// clip_atomic_writer_outbox.go, clip_atomic_writer_tracks.go, and
// clip_atomic_writer_cues.go. These helpers are shared by
// SQLiteMediaCommitter (canonical_clip_writer.go) and the legacy
// ClipMetadataWriterAdapter.
//
// PR-SINGLE-WRITER (August 2026): the 8 clip_atomic_writer*.go files
// were eliminated after the migration of CommitClipAndIndexEvent +
// CommitClipTextAndIndexEvent onto SQLiteMediaCommitter. The helpers
// survived because they are consumed by multiple non-adapter callers
// (asset_committer.go, clip_metadata_writer.go).
package testsupport

import (
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// ── Column-mapping derivation helpers (from clip_atomic_writer_asset.go) ─

// clipTagsJSON marshals the clip tag list as a JSON array string for the
// media_assets.tags column (empty slice → NULL-compatible empty string).
func clipTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	raw, _ := json.Marshal(tags)
	return string(raw)
}

// clipTagsNorm derives the media_assets.tags_norm search string: the
// space-joined lowercase tag list (same convention as the image repo's
// normalizeTags). Empty for an empty tag list.
func clipTagsNorm(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToLower(t))
	}
	return b.String()
}

// deriveNameFromAsset returns a canonical name for the clip row.
// Pulls from asset.Metadata.Summary if non-empty, otherwise falls
// back to the asset ID.

// deriveFilenameFromAsset returns the canonical filename for the
// clip row. Builds from the slug (asset.Metadata.Summary) if present,
// otherwise falls back to the canonical yt_<videoID>_<start>_<end>
// shape derived from the asset Coordinates. The full policy-versioned
// filename is set on the use case side via BuildClipFilename; the
// writer's filename is the basename of the local file when available.

// derivePolicyVersion extracts the policy_version suffix from a
// canonical clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>").
// Returns "v1" when the suffix is missing.

// deriveSourceVersion returns the canonical ingest-time content hash
// fingerprint used as event.source_version. In priority order:
//  1. asset.LegacyFileMD5 (the canonical MD5 of the local clip file).
//  2. fallback = MD5(clipID + ":" + policyVersion) — invariant under
//     retries so ON CONFLICT(event_key) collapses into a single row.

// filepathBase is a thin wrapper around path/filepath.Base.

// ── Outbox helpers (from clip_atomic_writer_outbox.go) ──────────────

// isTerminalOutboxStatus reports whether an outbox row's status is terminal.
// Delegates to the canonical predicate OWNED by internal/kernel/event so the
// test double cannot disagree with production about the lifecycle set.
func isTerminalOutboxStatus(status string) bool {
	return event.IsTerminalOutboxStatus(status)
}

// checkOutboxTerminalAfterCommit inspects the outbox enqueue result
// AFTER the orchestrator has called tx.Commit(). If the event was
// suppressed by an existing terminal row, this helper returns the
// BLOCKER #4 typed-error sentinel.

// ── Text track helpers (from clip_atomic_writer_tracks.go) ───────────

// localizedClipTextsToTextTracks converts payload-provided
// LocalizedClipText entries into domain TextTrack rows.

// upsertTextTracksReturningIDsInTx performs the asset_text_tracks
// UPSERT inside the caller's tx, capturing the assigned track_id
// (via RETURNING id) for each row.

// textTrackKey is the canonical key used to match TimedTextTrack
// entries with their parent TextTrack rows.

// ── Cue segment helpers (from clip_atomic_writer_cues.go) ────────────

// insertTextTrackSegmentsInTx performs the BATCH INSERT of
// asset_text_track_segments, one row per cue.
