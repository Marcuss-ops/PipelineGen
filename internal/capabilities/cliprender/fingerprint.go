// fingerprint.go owns the deterministic request fingerprint for the
// clip.render cache and batch deduplication.
//
// The fingerprint is the SHA-256 hex digest of the CANONICAL normalized
// RenderRequest JSON — one fact, one owner. It is what makes a
// repeated POST with identical semantics address the SAME bytes without
// re-rendering: the handler/worker can map fingerprint → certified
// artifact locator, and a batch can collapse N identical items to one
// GPU slot.
//
// Stability contract: same normalized request → same fingerprint,
// byte-identical across handler and worker (both Normalize before
// hashing). Unknown fields are rejected before hashing (strict decode),
// so the fingerprint is never taken over drifted input.
//
// ── Cache-bypass controls are NOT part of the identity ────────────────
//
// RenderRequest deliberately carries no force_refresh field, and the
// generation-level force_refresh (kernel/script GenerationEnvelopeV2, which
// bypasses the script idempotency store, the active key and the source/asset
// refresh) governs GENERATION identity, not RENDER identity — the same rule
// kernel/script/cache_key.go states for the script cache key ("ForceRefresh:
// cache-bypass control, not identity").
//
// Two consequences follow, and both are intended:
//
//   - the same visual contract is ONE render however many times it is
//     requested, which is exactly what makes this digest usable as a cache and
//     batch-dedup key;
//   - force_refresh does NOT bypass the render cache. An operator who wants
//     different bytes must change what is rendered — a new request semantics is
//     a new fingerprint by construction. A bypass flag inside the identity would
//     make the identity depend on how the render was requested rather than on
//     what it produces, and two jobs asking for the same bytes would then
//     address two cache entries.
package cliprender

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Fingerprint returns the canonical SHA-256 hex digest of the normalized
// request. The receiver is normalized idempotently before hashing so
// callers do not need to pre-normalize; the hash is stable regardless of
// field ordering on the wire because it is taken over the marshaled
// canonical struct, not the raw body.
func (r *RenderRequest) Fingerprint() (string, error) {
	if r == nil {
		return "", fmt.Errorf("clip.render fingerprint: request is nil")
	}
	// Work on a copy so the caller's mutation is not observable beyond
	// the hash (Normalize is idempotent, but copying keeps the contract
	// explicit).
	cp := *r
	cp.Normalize()
	if err := cp.Validate(); err != nil {
		return "", fmt.Errorf("clip.render fingerprint: normalized request invalid: %w", err)
	}
	// Canonical JSON projection: only the fields that affect the rendered
	// bytes. Destination drive folder and idempotency headers are
	// intentionally excluded — the same render published to two folders
	// is still one render (the Drive outbox handles fan-out). Queue
	// correlation IDs are also excluded.
	type fp struct {
		SourceAssetID string           `json:"source_asset_id"`
		Background    *BackgroundSpec  `json:"background"`
		Watermark     *WatermarkSpec   `json:"watermark"`
		Transcript    *TranscriptSpec  `json:"transcript"`
		Subtitles     *SubtitlesSpec   `json:"subtitles"`
		Output        *OutputSpec      `json:"output"`
		Audio         *AudioSpec       `json:"audio"`
		Overlays      []OverlayRefSpec `json:"overlays,omitempty"`
		Execution     *ExecutionSpec   `json:"execution,omitempty"`
	}
	canonical := fp{
		SourceAssetID: cp.SourceAssetID,
		Background:    cp.Background,
		Watermark:     cp.Watermark,
		Transcript:    cp.Transcript,
		Subtitles:     cp.Subtitles,
		Output:        cp.Output,
		Audio:         cp.Audio,
		Overlays:      cp.Overlays,
		Execution:     cp.Execution,
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("clip.render fingerprint: marshal canonical: %w", err)
	}
	return digest.SHA256Bytes(b), nil
}

// BatchFingerprint returns a deterministic batch-level digest over the
// ordered list of per-clip fingerprints. It is used for batch idempotency
// (same ordered clips → same batch id) and for logging.
func BatchFingerprint(perClip []string) string {
	joined := ""
	for i, fp := range perClip {
		if i > 0 {
			joined += "|"
		}
		joined += fp
	}
	return digest.SHA256String(joined)
}
