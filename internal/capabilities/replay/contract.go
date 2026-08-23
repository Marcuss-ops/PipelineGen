// Package replay owns the replay bundle contract: the durable snapshot of
// everything needed to re-execute a render deterministically without going
// back through research/script/clips/audio planning.
//
// The bundle captures the sealed render.RenderPlan, its canonical PlanSHA256,
// the exact execution environment (renderer, Rust protocol, FFmpeg, encoder
// policy) and the content-addressable identity of every asset. Assets are
// stored as SHA-256 + CAS URI — NEVER local paths: a path is a staging detail
// that means nothing after a restart or VPS migration. At replay the assets
// are materialized back into fresh staging files and re-verified byte-for-byte
// before the execution paths are rewritten in memory.
//
// Replay ≠ re-generation. Replay enters the SAME deterministic engine
// (fingerprint → cache → checkpoint → strategy resolver → executor) with
// zero LLM, zero research, zero clip search and zero editorial choices.
package replay

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// BundleVersion is the schema version of ReplayBundle. Bump it when the JSON
// shape or semantics change so bundles saved before the change are never
// silently misread after it.
const BundleVersion = "replay-bundle.v1"

var (
	ErrInvalidBundle = errors.New("invalid replay bundle")
	ErrNotWired      = errors.New("replay bundle store: not wired")
)

// ReplayAsset is the durable, content-addressable identity of one asset the
// render needs. CASURI is the storage reference (canonical "cas://<sha256>");
// SizeBytes is informational (0 = unknown) — integrity always comes from the
// SHA-256, never from the recorded size.
type ReplayAsset struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	CASURI    string `json:"cas_uri"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ReplayBundle is the complete, self-contained replay snapshot. It embeds the
// sealed render plan (the single canonical plan, never a parallel manifest)
// plus the exact execution environment that produced it. EncoderPolicyHash is
// empty for legacy plans that carry no execution policy.
type ReplayBundle struct {
	Version       string            `json:"version"`
	OriginalJobID string            `json:"original_job_id"`
	RenderPlan    render.RenderPlan `json:"render_plan"`
	PlanSHA256    string            `json:"plan_sha256"`

	RendererVersion     string `json:"renderer_version"`
	RustProtocolVersion string `json:"rust_protocol_version"`
	FFmpegVersion       string `json:"ffmpeg_version"`
	EncoderPolicyHash   string `json:"encoder_policy_hash,omitempty"`

	Assets    []ReplayAsset `json:"assets"`
	CreatedAt time.Time     `json:"created_at"`
}

// CanonicalCASURI returns the canonical storage reference for a SHA-256:
// the address IS the digest.
func CanonicalCASURI(sha256hex string) string {
	return "cas://" + sha256hex
}

// BuildAssets projects the render plan into durable replay assets: every
// manifest entry and the final audio (when present) become a content-addressed
// ReplayAsset, with local paths dropped. Deduplicated by asset id.
func BuildAssets(plan render.RenderPlan) []ReplayAsset {
	seen := make(map[string]struct{}, len(plan.Manifest)+1)
	assets := make([]ReplayAsset, 0, len(plan.Manifest)+1)
	for _, entry := range plan.Manifest {
		if _, ok := seen[entry.AssetID]; ok {
			continue
		}
		seen[entry.AssetID] = struct{}{}
		assets = append(assets, ReplayAsset{
			AssetID: entry.AssetID,
			SHA256:  entry.SHA256,
			CASURI:  CanonicalCASURI(entry.SHA256),
		})
	}
	if plan.FinalAudio != nil {
		if _, ok := seen[plan.FinalAudio.AssetID]; !ok {
			assets = append(assets, ReplayAsset{
				AssetID:   plan.FinalAudio.AssetID,
				SHA256:    plan.FinalAudio.SHA256,
				CASURI:    CanonicalCASURI(plan.FinalAudio.SHA256),
				SizeBytes: plan.FinalAudio.SizeBytes,
			})
		}
	}
	return assets
}

// Validate fails closed on a structurally incomplete or inconsistent bundle:
// a bundle that cannot deterministically identify its plan and environment
// must never be persisted.
func (b ReplayBundle) Validate() error {
	if b.Version != BundleVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidBundle, b.Version)
	}
	if strings.TrimSpace(b.OriginalJobID) == "" {
		return fmt.Errorf("%w: original_job_id is required", ErrInvalidBundle)
	}
	if !isSHA256(b.PlanSHA256) {
		return fmt.Errorf("%w: plan_sha256 must be a valid SHA256", ErrInvalidBundle)
	}
	if b.PlanSHA256 != b.RenderPlan.PlanSHA256 {
		return fmt.Errorf("%w: plan_sha256 %q does not match render plan %q", ErrInvalidBundle, b.PlanSHA256, b.RenderPlan.PlanSHA256)
	}
	if err := b.RenderPlan.Validate(); err != nil {
		return fmt.Errorf("%w: render plan: %v", ErrInvalidBundle, err)
	}
	if strings.TrimSpace(b.RendererVersion) == "" {
		return fmt.Errorf("%w: renderer_version is required", ErrInvalidBundle)
	}
	if strings.TrimSpace(b.RustProtocolVersion) == "" {
		return fmt.Errorf("%w: rust_protocol_version is required", ErrInvalidBundle)
	}
	if strings.TrimSpace(b.FFmpegVersion) == "" {
		return fmt.Errorf("%w: ffmpeg_version is required", ErrInvalidBundle)
	}
	if b.EncoderPolicyHash != "" && !isSHA256(b.EncoderPolicyHash) {
		return fmt.Errorf("%w: encoder_policy_hash must be a valid SHA256", ErrInvalidBundle)
	}
	for i, asset := range b.Assets {
		if strings.TrimSpace(asset.AssetID) == "" || !isSHA256(asset.SHA256) || strings.TrimSpace(asset.CASURI) == "" {
			return fmt.Errorf("%w: asset[%d] requires asset_id, sha256 and cas_uri", ErrInvalidBundle, i)
		}
		if asset.SizeBytes < 0 {
			return fmt.Errorf("%w: asset[%d] size_bytes must not be negative", ErrInvalidBundle, i)
		}
	}
	if b.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidBundle)
	}
	return nil
}

// BundleStore is the durable replay bundle port. Save upserts the bundle for
// its original job (the latest canonical snapshot wins); Get returns
// (nil, nil) when the job has no saved bundle.
type BundleStore interface {
	Save(ctx context.Context, bundle ReplayBundle) error
	Get(ctx context.Context, originalJobID string) (*ReplayBundle, error)
}

// MaterializedAsset is one replay asset restored from durable storage into a
// fresh local staging file, with its bytes re-verified against the recorded
// SHA-256.
type MaterializedAsset struct {
	AssetID   string
	SHA256    string
	LocalPath string
	SizeBytes int64
}

// AssetSource materializes replay assets from durable storage into local
// staging files and verifies their bytes. It never trusts the recorded row:
// the SHA-256 is re-computed from the materialized bytes (fail-closed).
type AssetSource interface {
	Materialize(ctx context.Context, asset ReplayAsset) (MaterializedAsset, error)
}

func isSHA256(value string) bool {
	if len(value) != digest.SHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
