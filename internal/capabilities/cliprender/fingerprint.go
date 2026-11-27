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
// ── Input revisions (CRA-03) ──────────────────────────────────────────
//
// The identity carries the OPTIONAL revision pins of the inputs that decide
// the pixels: source_revision, background.revision, watermark.revision and
// transcript.revision. The rule, stated once here and enforced by Prepare:
//
//   - a request that pins a revision is addressed by that revision. Changing
//     the pinned revision changes the fingerprint (cache miss, new render);
//   - a pinned revision is verified against the bytes actually resolved
//     (asset content_sha256, transcript text hash). A mismatch or an
//     unverifiable pin fails closed with ErrInputRevisionMismatch — the
//     render never silently runs on different bytes;
//   - a request that pins nothing keeps the historical ID-only identity,
//     which REQUIRES the referenced asset to be immutable per ID. The
//     PostgreSQL cache still verifies the OUTPUT sha256; the pin is what
//     extends that verification to the inputs.
//
// Revisions stay optional on purpose: making them mandatory would break every
// existing payload and every caller that cannot resolve a digest before the
// first render. An unpinned caller keeps its hit rate; a caller that wants
// revision-safe identity opts in without a second endpoint.
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

// RendererLoweringVersion is part of the render identity on purpose. The
// fingerprint hashes the REQUEST, so a change in the lowering — grid geometry,
// effect mapping, paint order — would otherwise keep addressing artifacts
// produced by the previous code: same payload, different pixels, stale bytes.
// Bump this string in the same change that alters what a request renders; the
// golden in TestFingerprint_PinsTheLoweringVersion fails until it is updated.
const RendererLoweringVersion = "cliprender-lowering-2026-11-27"

// Fingerprint returns the canonical SHA-256 hex digest of the normalized
// request, destination included. It is the DELIVERY-AWARE cache key: a hit
// returns the asset previously published to that exact destination.
func (r *RenderRequest) Fingerprint() (string, error) {
	return r.fingerprintCanonical(true)
}

// RenderFingerprint returns the DESTINATION-INDEPENDENT identity of the same
// render (CRA-04). Two requests that differ only in drive_folder_id/subfolder
// produce the same render fingerprint: the pixels are identical, so the
// certified artifact may be reused and published to the second destination
// without another GPU pass. It is deliberately NOT the cache key a hit may
// answer with — that stays Fingerprint.
func (r *RenderRequest) RenderFingerprint() (string, error) {
	return r.fingerprintCanonical(false)
}

// fingerprintCanonical projects the request onto the fields that decide the
// rendered bytes (and, when includeDestination is true, where they are
// delivered), then hashes the canonical JSON.
func (r *RenderRequest) fingerprintCanonical(includeDestination bool) (string, error) {
	if r == nil {
		return "", fmt.Errorf("clip.render fingerprint: request is nil")
	}
	// Work on a DEEP copy: Normalize() writes through the nested pointers
	// (Background.Mode, Watermark.opacitySet, Transcript.persistSet, …), so a
	// shallow struct copy would still mutate the caller. Hashing must never
	// change the request it measures.
	cp := r.cloneForFingerprint()
	cp.Normalize()
	// Revision pins are digests: a sha256: prefix or uppercase spelling is the
	// same revision, so canonicalize before hashing. Validation still rejects a
	// malformed pin at the request boundary; this only removes spelling drift
	// from the identity.
	cp.SourceRevision = normalizeSHA256(cp.SourceRevision)
	if cp.Background != nil {
		cp.Background.Revision = normalizeSHA256(cp.Background.Revision)
	}
	if cp.Watermark != nil {
		cp.Watermark.Revision = normalizeSHA256(cp.Watermark.Revision)
	}
	if cp.Transcript != nil {
		cp.Transcript.Revision = normalizeSHA256(cp.Transcript.Revision)
	}
	// Fingerprint deliberately does NOT Validate. Callers validate at their own
	// boundary (HTTP handler, worker payload, batch item) and then reuse this
	// digest; validating here repeated the contract resolver and made one cold
	// request pay five Normalize/Validate/Fingerprint passes (CR-07). An invalid
	// request is rejected by Validate before it can be enqueued or cached.
	// Canonical JSON projection: the fields that affect the rendered bytes
	// AND the fields that decide where those bytes are delivered.
	//
	// The destination is part of the identity, not routing trivia: the
	// deterministic cache is keyed on this digest and a hit returns the
	// previously PUBLISHED asset, so a request that differs only in its Drive
	// destination must not be served the other destination's artifact. An
	// earlier revision excluded drive_folder_id and every cache hit crossed
	// delivery folders silently (ticket CR-19-03). Queue correlation IDs and
	// idempotency headers stay excluded: they identify a submission, not a
	// deliverable.
	// Project only what can change the rendered bytes or the published
	// destination. A disabled block is not part of the visual contract: its
	// position, margins, style or mode are inert and must not split one render
	// into two cache entries (CR-01).
	background := cp.Background
	if background != nil && background.Mode == BackgroundModeNone {
		background = nil
	}
	watermark := cp.Watermark
	if watermark != nil && !watermark.Enabled {
		watermark = nil
	}
	subtitles := cp.Subtitles
	if subtitles != nil && !subtitles.Enabled {
		subtitles = nil
	}

	// Execution is deliberately NOT render identity. Chronon is the only
	// backend and is GPU-native, so require_gpu changes an assertion, not the
	// pixels; require_zero_copy is rejected at Validate. Including it made two
	// equivalent requests miss the same cache entry. Destination stays in the
	// identity because a hit returns the previously published asset.
	type fp struct {
		RendererVersion string           `json:"renderer_version"`
		SourceAssetID   string           `json:"source_asset_id"`
		SourceRevision  string           `json:"source_revision,omitempty"`
		Background      *BackgroundSpec  `json:"background,omitempty"`
		Watermark       *WatermarkSpec   `json:"watermark,omitempty"`
		Transcript      *TranscriptSpec  `json:"transcript"`
		Subtitles       *SubtitlesSpec   `json:"subtitles,omitempty"`
		Output          *OutputSpec      `json:"output"`
		Audio           *AudioSpec       `json:"audio"`
		Overlays        []OverlayRefSpec `json:"overlays,omitempty"`
		DriveFolderID   string           `json:"drive_folder_id,omitempty"`
		SubfolderName   string           `json:"subfolder_name,omitempty"`
	}
	canonical := fp{
		RendererVersion: RendererLoweringVersion,
		SourceAssetID:   cp.SourceAssetID,
		SourceRevision:  normalizeSHA256(cp.SourceRevision),
		Background:      background,
		Watermark:       watermark,
		Transcript:      cp.Transcript,
		Subtitles:       subtitles,
		Output:          cp.Output,
		Audio:           cp.Audio,
		Overlays:        cp.Overlays,
	}
	if includeDestination {
		canonical.DriveFolderID = cp.Destination.DriveFolderID
		canonical.SubfolderName = cp.Destination.SubfolderName
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("clip.render fingerprint: marshal canonical: %w", err)
	}
	return digest.SHA256Bytes(b), nil
}

// cloneForFingerprint deep-copies every pointer-bearing block Normalize can
// write, plus the slices the canonical projection reads. Nested style blocks
// are copied so a future Normalize that touches them cannot mutate the caller
// either. It is deliberately narrower than a general Clone: the fingerprint
// only needs the blocks it hashes.
func (r *RenderRequest) cloneForFingerprint() *RenderRequest {
	cp := *r
	if r.Background != nil {
		block := *r.Background
		cp.Background = &block
	}
	if r.Watermark != nil {
		block := *r.Watermark
		if r.Watermark.Style != nil {
			style := *r.Watermark.Style
			block.Style = &style
		}
		cp.Watermark = &block
	}
	if r.Transcript != nil {
		block := *r.Transcript
		cp.Transcript = &block
	}
	if r.Subtitles != nil {
		block := *r.Subtitles
		if r.Subtitles.Style != nil {
			style := *r.Subtitles.Style
			block.Style = &style
		}
		cp.Subtitles = &block
	}
	if r.Output != nil {
		block := *r.Output
		if r.Output.SourceFrame != nil {
			frame := *r.Output.SourceFrame
			block.SourceFrame = &frame
		}
		if r.Output.Grid != nil {
			grid := *r.Output.Grid
			block.Grid = &grid
		}
		if r.Output.Effects != nil {
			block.Effects = append([]ClipEffectSpec(nil), r.Output.Effects...)
		}
		cp.Output = &block
	}
	if r.Audio != nil {
		block := *r.Audio
		cp.Audio = &block
	}
	if r.Destination != nil {
		block := *r.Destination
		cp.Destination = &block
	}
	if r.Execution != nil {
		block := *r.Execution
		cp.Execution = &block
	}
	if r.Overlays != nil {
		cp.Overlays = append([]OverlayRefSpec(nil), r.Overlays...)
	}
	return &cp
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
