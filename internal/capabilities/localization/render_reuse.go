package localization

// render_reuse.go owns the content-addressed reuse seam for localized renders:
// "the same plan must not be rendered twice".
//
// LocalizedClipPlan.Fingerprint is already the canonical identity of the bytes a
// variant produces — fingerprint.go states it outright ("a re-run can skip
// translate/ASS/render/upload when nothing changed") — and the localization
// runner already used it for the Drive file identity. The one step that never
// consulted it was the RENDER: every re-run paid the full Chronon wall for bytes
// it had already certified. The crate's own critical-path ticket names this the
// largest single win available for repeated work.
//
// WHAT A HIT SAVES: the compile→Submit→Settle→materialize→hash half of the work,
// i.e. the GPU. WHAT A HIT DOES NOT SAVE: the subtitle wiring. A reuse hit still
// resolves the plan's translated track, re-checks its hash and language, and
// recompiles that language's ASS before the cached bytes are accepted, so a hit
// can never ride on a stale or wrong-language subtitle artifact.
//
// SAFETY DIRECTION: reuse is fail-SOFT and verified, never trusted.
//   - a nil cache, a lookup error, or an absent entry ⇒ MISS (render);
//   - a cached record whose local file is gone, whose size changed, or whose
//     bytes no longer hash to the certified digest ⇒ MISS (render);
//   - a Store failure after a successful render ⇒ the certified artifact is
//     still returned (the cache is an optimization, not the authority).
//
// The render is always the authoritative path; the cache only skips it when it
// can prove it is holding the very bytes that path already produced.

import (
	"context"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// RenderSource names WHERE a certified artifact's bytes came from. It is the
// difference between "0 s of GPU because the deterministic cache held these
// bytes" and "the worker reported nothing": a dedup re-run and an unmeasured
// fresh render both produced `metrics: null` with a short wall, and no consumer
// could tell them apart. Every localized artifact therefore states its source
// explicitly instead of leaving it to be inferred from a missing metric map.
const (
	// RenderSourceFreshGPU is a render that actually executed the Chronon
	// boundary in this process.
	RenderSourceFreshGPU = "fresh_gpu"
	// RenderSourceDeterministicCache is a render skipped because the same plan
	// fingerprint's bytes were already certified and re-hashed successfully.
	RenderSourceDeterministicCache = "deterministic_render_cache"
)

// ReusedRenderArtifact is the certified output of a previous render of the SAME
// plan fingerprint, as far as a reuse consumer can verify it locally.
type ReusedRenderArtifact struct {
	// Fingerprint is the LocalizedClipPlan.Fingerprint the artifact belongs to.
	Fingerprint string
	// LocalPath is where the certified bytes currently are. It is a
	// producer-process runtime value, never a serialized identity field: the
	// tag keeps SHA256 as the only identity this record publishes (godlike/06,
	// one owner per fact).
	LocalPath string `json:"-"`
	// SHA256 is the certified content digest of those bytes.
	SHA256 string
	// SizeBytes is the certified size of those bytes.
	SizeBytes int64
	// DurationMS is the certified clip duration.
	DurationMS int64
	// VideoCodec / AudioCodec / Backend are the certified media facts.
	VideoCodec string
	AudioCodec string
	Backend    string
}

// RenderReuseCache resolves a plan fingerprint to the artifact a previous render
// certified for it. Implementations live outside the capability (an adapter over
// the canonical fingerprint→locator render cache, for instance); the capability
// never queries a database itself.
type RenderReuseCache interface {
	// Lookup returns (artifact, true, nil) on a hit, (nil, false, nil) on a
	// miss, and (nil, false, err) when the cache itself failed.
	Lookup(ctx context.Context, fingerprint string) (*ReusedRenderArtifact, bool, error)
	// Store records a freshly certified artifact under its fingerprint.
	Store(ctx context.Context, artifact ReusedRenderArtifact) error
}

// WithRenderReuseCache attaches the optional reuse cache. A nil cache (or a nil
// receiver) leaves the renderer exactly as it was: every plan renders.
func (r *LocalizedClipRenderer) WithRenderReuseCache(cache RenderReuseCache) *LocalizedClipRenderer {
	if r == nil {
		return r
	}
	r.reuseCache = cache
	return r
}

// reusedArtifact resolves a cache hit into the RENDERED artifact the cached
// bytes certify, or ok=false when anything at all prevents reuse. ass is the
// subtitle artifact this call already wired and verified; it is carried onto the
// reused artifact so provenance (and the optional sidecar upload) still names the
// language's own ASS.
func (r *LocalizedClipRenderer) reusedArtifact(ctx context.Context, plan LocalizedClipPlan, ass *SubtitleAsset) (LocalizedClipArtifact, bool) {
	if r == nil || r.reuseCache == nil {
		return LocalizedClipArtifact{}, false
	}
	cached, hit, err := r.reuseCache.Lookup(ctx, plan.Fingerprint)
	if err != nil || !hit || cached == nil {
		// Fail-soft: an unavailable or erroneous cache is a miss, never a
		// failed render. The render below is the authoritative path.
		return LocalizedClipArtifact{}, false
	}
	if !reusableBytes(cached) {
		return LocalizedClipArtifact{}, false
	}
	artifact := LocalizedClipArtifact{
		Version:         LocalizedClipArtifactVersion,
		JobID:           plan.JobID,
		SceneID:         plan.SceneID,
		ClipID:          plan.ClipID,
		Language:        plan.TargetLanguage,
		PlanFingerprint: plan.Fingerprint,
		LocalPath:       cached.LocalPath,
		SHA256:          strings.ToLower(cached.SHA256),
		SizeBytes:       cached.SizeBytes,
		DurationMS:      cached.DurationMS,
		VideoCodec:      cached.VideoCodec,
		AudioCodec:      cached.AudioCodec,
		Backend:         cached.Backend,
		Status:          LocalizedClipRendered,
		// A hit is a FACT about this artifact, not an absence of telemetry:
		// without it a reused clip is indistinguishable from a fresh render
		// that reported no metrics at all.
		Reused:       true,
		RenderSource: RenderSourceDeterministicCache,
	}
	if ass != nil {
		artifact.SubtitlePath = ass.LocalPath
		artifact.SubtitleSHA256 = ass.SHA256
	}
	return artifact, true
}

// reusableBytes verifies that the cached record still describes bytes on disk:
// the file exists, is not a directory, has the certified size, and hashes to the
// certified digest. Anything else is a miss — a truncated, replaced or vanished
// artifact must be re-rendered, never published.
//
// The hash pass costs one read of the artifact (tens of ms on a clip); the render
// it replaces costs seconds of GPU. Paying it is what makes "reuse" a claim about
// the BYTES instead of a claim about a filename.
func reusableBytes(cached *ReusedRenderArtifact) bool {
	if cached == nil {
		return false
	}
	if strings.TrimSpace(cached.LocalPath) == "" || !isSHA256Hex(cached.SHA256) || cached.SizeBytes <= 0 {
		return false
	}
	info, err := os.Stat(cached.LocalPath)
	if err != nil || info.IsDir() || info.Size() != cached.SizeBytes {
		return false
	}
	sha, size, err := digest.SHA256File(cached.LocalPath)
	if err != nil || size != cached.SizeBytes {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(sha), strings.TrimSpace(cached.SHA256))
}

// storeRendered records a freshly certified render under the plan's fingerprint,
// so the next identical localization can skip the GPU.
//
// A Store failure is deliberately swallowed: the artifact in hand is already
// certified and complete, and failing the run because the OPTIMIZATION could not
// be recorded would trade a working render for nothing.
func (r *LocalizedClipRenderer) storeRendered(ctx context.Context, plan LocalizedClipPlan, facts RenderFacts) {
	if r == nil || r.reuseCache == nil {
		return
	}
	_ = r.reuseCache.Store(ctx, ReusedRenderArtifact{
		Fingerprint: plan.Fingerprint,
		LocalPath:   facts.LocalPath,
		SHA256:      facts.SHA256,
		SizeBytes:   facts.SizeBytes,
		DurationMS:  facts.DurationMS,
		VideoCodec:  facts.VideoCodec,
		AudioCodec:  facts.AudioCodec,
		Backend:     facts.Backend,
	})
}
