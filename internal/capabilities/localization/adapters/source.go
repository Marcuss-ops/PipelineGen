package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// MediaProber is the narrow probe seam the source resolver needs to derive
// the rational frame rate. The concrete *rustexec.VideoProcessor satisfies it.
type MediaProber interface {
	Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
}

// SourceResolver implements localization.SourceResolver by composing the
// canonical clip.render registry + materializer adapters with a media probe.
// It is immutable after construction and safe for concurrent ResolveSource
// calls.
type SourceResolver struct {
	resolve  cliprender.AssetResolver
	material cliprender.AssetMaterializer
	probe    MediaProber
}

// NewSourceResolver builds the resolver. Fail-closed at call time (not
// construction): the nil checks live in ResolveSource so the composition root
// can build adapters independently of wiring order.
func NewSourceResolver(resolve cliprender.AssetResolver, material cliprender.AssetMaterializer, probe MediaProber) *SourceResolver {
	return &SourceResolver{resolve: resolve, material: material, probe: probe}
}

var _ localization.SourceResolver = (*SourceResolver)(nil)

// ResolveSource resolves the source clip into verified render facts: the local
// path, the verified content SHA-256 (read from the actual bytes), and the
// rational frame rate. Fail-closed: an unknown asset, a hash mismatch against
// the registry, an unreadable file, or a missing frame rate is a typed error.
func (r *SourceResolver) ResolveSource(ctx context.Context, assetID string) (localization.SourceFacts, error) {
	if r == nil || r.resolve == nil || r.material == nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: source resolver not wired")
	}

	ref, err := r.resolve.ResolveAsset(ctx, assetID)
	if err != nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: resolve source asset %q: %w", assetID, err)
	}
	if ref == nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: source asset %q not found", assetID)
	}

	mat, err := r.material.Materialize(ctx, *ref)
	if err != nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: materialize source asset %q: %w", assetID, err)
	}
	if mat == nil || strings.TrimSpace(mat.LocalPath) == "" || mat.SHA256 == "" {
		return localization.SourceFacts{}, fmt.Errorf("localization: materialized source asset %q is incomplete", assetID)
	}

	// godlike/07 fail-closed: the materialized bytes must match the registry's
	// recorded hash. A non-clean registry hash (empty, prefixed, or non-hex)
	// is skipped — the computed digest is still returned and the compiler's
	// plan.SourceSHA256 gate remains the authoritative check.
	if reg := cleanSHA256(ref.LegacyFileMD5); reg != "" && reg != mat.SHA256 {
		return localization.SourceFacts{}, fmt.Errorf("localization: source asset %q hash mismatch: registry %q, local %q", assetID, reg, mat.SHA256)
	}

	if r.probe == nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: media probe not wired (source asset %q)", assetID)
	}
	info, err := r.probe.Probe(ctx, mat.LocalPath)
	if err != nil {
		return localization.SourceFacts{}, fmt.Errorf("localization: probe source asset %q: %w", assetID, err)
	}
	if info == nil || info.FPSNum <= 0 || info.FPSDen <= 0 {
		return localization.SourceFacts{}, fmt.Errorf("localization: source asset %q has no valid rational frame rate", assetID)
	}

	return localization.SourceFacts{
		AssetID:    ref.AssetID,
		LocalPath:  mat.LocalPath,
		SHA256:     mat.SHA256,
		DurationMS: mat.DurationMS,
		FrameRate:  audio.FrameRate{Numerator: int64(info.FPSNum), Denominator: int64(info.FPSDen)},
	}, nil
}

// cleanSHA256 returns the value when it is a canonical 64-character lowercase
// hex SHA-256, and "" otherwise. Used to decide whether the registry hash is
// comparable to the computed digest (a prefixed/legacy hash must not produce a
// false mismatch).
func cleanSHA256(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return ""
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return s
}
