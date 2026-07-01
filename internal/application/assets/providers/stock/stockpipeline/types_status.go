// Package stockpipeline — types_status.go (Audit P0 #6, July 2026).
//
// IndexingStatus is the typed 4-state enum that replaces the legacy
// `Indexed bool` on ChunkResult. The pre-refactor bool was both
// silently false-positive (assetIndex nil ⇒ Indexed=true ⇒ false
// completion signal) AND silently false-negative (UpdateSearchTerms
// failure logged + dispatch continued). Audit P0 #6 closes both
// silent-success classes by making the indexer's lifecycle observable
// end-to-end via 4 grep-friendly constants.
package stockpipeline

import (
	"encoding/json"
	"strconv"
)

// IndexingStatus is the typed enum for the post-upload indexing
// lifecycle of a single stock-pipeline chunk. Constants are
// discriminated by string value so log greppers (`rg IndexingFailed`)
// and selectors in observability dashboards can pivot on the surface.
//
// The 4 states model the OUTER outcome of the indexing surface for
// one chunk, not the per-step status. The chunk itself is on Drive
// in all states except the default-zero (IndexingPending, before the
// indexing surface has run once).
type IndexingStatus string

const (
	// IndexingPending is the default-zero state, set on a freshly
	// constructed ChunkResult before the indexing surface has run.
	// Operators never see this in production chunks because
	// `uploadAndIndexChunk` always sets a non-pending state before
	// returning.
	IndexingPending IndexingStatus = "indexing_pending"

	// IndexingSkipped is set when the canonical post-upload indexing
	// surface is NOT wired (e.g. assetIndex port is nil in a test
	// fixture or partial deploy). Distinguishable from Failed so
	// operators can tell "we never tried" from "we tried + it broke".
	IndexingSkipped IndexingStatus = "indexing_skipped"

	// IndexingCompleted is the terminal success state — the asset_index
	// upsert + clips-repo UpdateSearchTerms + outbox EnqueueAndIndex
	// all succeeded.
	IndexingCompleted IndexingStatus = "indexing_completed"

	// IndexingFailed is the terminal failure state — at least one of
	// asset_index.Upsert, clips-repo.UpdateSearchTerms, or
	// outbox.EnqueueAndIndex returned an error. The chunk itself is
	// still on Drive (uploadAndIndexChunk returns nil at the upload
	// level because Indexed is best-effort at the run level); operator
	// can backfill from ChunkResult.DriveFileID.
	IndexingFailed IndexingStatus = "indexing_failed"
)

// MarshalJSON preserves the LEGACY wire shape (`true|false`) so the
// public JSON contract for ChunkResult.Indexed is unchanged.
// Mapping: IndexingCompleted → true; everything else → false.
//
// The lossy encoding is intentional: external API consumers don't
// currently distinguish failure-from-deferred indexing; they only
// need to know "did the chunk's post-upload indexing reach the
// IndexingCompleted terminal". The 4-state enum is an internal-Go
// surface improvement; the external JSON stays bool for backwards
// compat with the existing registered clients.
func (s IndexingStatus) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatBool(s == IndexingCompleted)), nil
}

// UnmarshalJSON accepts both the legacy bool shape AND the typed
// string shape — callers that sent `true|false` historically are
// still readable (legacy: true→Completed, false→Pending); callers
// emitting the typed string constants round-trip exactly.
//
// The `false → IndexingPending` mapping is the most lossy step of
// the transition: a legacy `false` historically meant "not done"
// without distinguishing skip vs fail vs pending. We map it to
// IndexingPending (= "the indexing surface didn't reach Completed",
// close enough). Newly-typed Go callers emitting the typed string
// constants don't lose information.
func (s *IndexingStatus) UnmarshalJSON(data []byte) error {
	// Quoted string shape: typed-string round-trip.
	if len(data) > 0 && data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*s = IndexingStatus(raw)
		return nil
	}
	// Bare bool shape: legacy compat.
	var b bool
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}
	if b {
		*s = IndexingCompleted
	} else {
		*s = IndexingPending
	}
	return nil
}

// Compile-time assertions — drift in future Go versions surfaces at
// compile, not runtime.
var (
	_ json.Marshaler   = IndexingStatus("")
	_ json.Unmarshaler = (*IndexingStatus)(nil)
)
