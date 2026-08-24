// Package assets — clip_atomic_writer_asset.go: media_assets
// column-mapping derivation helpers, split from the
// orchestrator clip_atomic_writer.go for per-table responsibility
// (clip_atomic_writer split, July 2026).
//
// godlike/10 SSOT helpers-as-receivers: this file holds pure-function
// string derivation for column values. No business branching —
// language provenance / policy version / file-hash fingerprints are
// computed here ONCE and reused by both CommitClipAndIndexEvent and
// CommitClipTextAndIndexEvent via direct calls. Provenance / language
// logic is not duplicated across helpers or entry points.
package assets

import (
	"encoding/json"
	"path/filepath"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
)

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

// ── Column-mapping derivation helpers (pure functions) ──────────────
//
// godlike/10 SSOT (helpers-as-receivers): these helpers exist solely
// to compute the column values for upsertClipInTx. They are NOT
// business branching — they take a ClipAsset / clipID and return
// the string that belongs in the corresponding INSERT column. They
// live here (next to upsertClipInTx) so the column-mapping
// contract is co-located with the SQL it feeds. They ARE called
// from both entry points (CommitClipAndIndexEvent and
// CommitClipTextAndIndexEvent) — provenance / language derivation
// is computed ONCE per call, never duplicated.

// deriveNameFromAsset returns a canonical name for the clip row.
// Pulls from asset.Metadata.Summary if non-empty, otherwise falls
// back to the asset ID. Kept private because the column mapping
// is an internal detail of the writer adapter.
func deriveNameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.Metadata.Summary != "" {
		return asset.Metadata.Summary
	}
	return ""
}

// deriveFilenameFromAsset returns the canonical filename for the
// clip row. Builds from the slug (asset.Metadata.Summary) if present,
// otherwise falls back to the canonical yt_<videoID>_<start>_<end>
// shape derived from the asset Coordinates. The full policy-versioned
// filename is set on the use case side via BuildClipFilename; the
// writer's filename is the basename of the local file when available.
func deriveFilenameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.LocalPath != "" {
		return filepathBase(asset.LocalPath)
	}
	return ""
}

// derivePolicyVersion extracts the policy_version suffix from a
// canonical clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>").
// Returns "v1" when the suffix is missing (legacy / build error /
// hand-crafted clipID) so the source-version fallback remains stable
// across retries.
func derivePolicyVersion(clipID string) string {
	const wantUnderscores = 4
	seen := 0
	for i := len(clipID) - 1; i >= 0; i-- {
		if clipID[i] == '_' {
			seen++
			if seen == wantUnderscores {
				pv := clipID[i+1:]
				if pv != "" {
					return pv
				}
				return "v1"
			}
		}
	}
	return "v1"
}

// deriveSourceVersion returns the canonical ingest-time content hash
// fingerprint used as event.source_version. In priority order:
//  1. asset.LegacyFileMD5 (the canonical MD5 of the local clip file).
//  2. fallback = MD5(clipID + ":" + policyVersion) — invariant under
//     retries so ON CONFLICT(event_key) collapses into a single row.
func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
	if fileHash != "" {
		return fileHash
	}
	return checksum.LegacyMD5String(clipID + ":" + policyVersion)
}

// filepathBase is a thin wrapper around path/filepath.Base that
// avoids importing path/filepath at the top of the file. The local
// path is always absolute in production, so the Base call is safe.
//
// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza): the local wrapper
// was removed in favour of stdlib path/filepath.Base per the
// code-reviewer critical finding. The previous hand-rolled loop
// had the same string contract on absolute Unix paths but missed
// Windows backslash handling and edge cases for trailing
// separators; stdlib's filepath.Base is the canonical implementation.
func filepathBase(p string) string {
	return filepath.Base(p)
}
