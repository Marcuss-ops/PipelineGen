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
	"path/filepath"
	"strings"
	"time"

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
	// StorageKey / ArtifactURL are the DURABLE locator of the same certified
	// bytes in RenderingGen's object store. They are what makes reuse survive
	// the cleanup of a local workspace or a move to another host: when
	// LocalPath is gone, the renderer can materialize the object and verify it
	// against SHA256 + SizeBytes instead of paying the GPU again. Empty for
	// records written before the columns existed; those remain local-only hits.
	StorageKey  string
	ArtifactURL string
	// DriveFileID / DriveLink / DriveFolderID are the Drive identity these
	// bytes were already published under. They are optional: a record written
	// before the first publication has none, and a deployment that keeps one
	// Drive copy per run never records them. When present they let a later run
	// reuse the published file instead of uploading identical bytes again
	// (2026-11-16 Obama runs: 403,5 MB of unchanged clips re-uploaded per run).
	DriveFileID   string
	DriveLink     string
	DriveFolderID string
}

// RenderReuseHydrator is the OPTIONAL half of the render boundary that can
// fetch a certified artifact from its durable locator into a local file. The
// reuse seam uses it when a cache row still names the bytes but the local copy
// is gone (workspace cleanup, another host). The hydrator MUST verify size and
// certified digest while streaming; the reuse seam re-verifies the file before
// accepting it, so a bad fetch is a miss, never a corrupted hit.
type RenderReuseHydrator interface {
	HydrateReusedRender(ctx context.Context, artifact *ReusedRenderArtifact, destPath string) error
}

// RenderDriveIdentity is the Drive identity a certified localized render is
// published under: the file id, its share link and the folder it lives in.
// FolderID is the folder that actually holds the file, not the destination the
// current run would have used.
type RenderDriveIdentity struct {
	FileID   string
	Link     string
	FolderID string
}

// RenderDriveIdentityRecorder is the optional half of RenderReuseCache that
// remembers where certified bytes were published. It is separate from Store
// because publication happens AFTER the render is certified (the render cache
// holds bytes, not destinations) and because a cache that cannot persist the
// identity must still be usable for render reuse.
type RenderDriveIdentityRecorder interface {
	RecordDriveIdentity(ctx context.Context, fingerprint string, identity RenderDriveIdentity) error
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

// WithRenderReuseDir attaches the scratch directory used to materialize a
// certified object-store artifact when a cache row's local file no longer
// exists. Without it the durable locator is not used and such a row is a miss.
func (r *LocalizedClipRenderer) WithRenderReuseDir(dir string) *LocalizedClipRenderer {
	if r == nil {
		return r
	}
	r.reuseDir = strings.TrimSpace(dir)
	return r
}

// reusedArtifact resolves a cache hit into the RENDERED artifact the cached
// bytes certify, or ok=false when anything at all prevents reuse. ass is the
// subtitle artifact this call already wired and verified; it is carried onto the
// reused artifact so provenance (and the optional sidecar upload) still names the
// language's own ASS.
func (r *LocalizedClipRenderer) reusedArtifact(ctx context.Context, plan LocalizedClipPlan, ass *SubtitleAsset) (LocalizedClipArtifact, bool) {
	if r == nil || r.reuseCache == nil {
		recordReuseMiss("cache_disabled")
		return LocalizedClipArtifact{}, false
	}
	// Key on the ASS digest too: the plan fingerprint pins the translated track
	// and style, but the ASS bytes are produced from them. A layout change can
	// keep the same track hash and still change the burned pixels, so a lookup
	// by fingerprint alone would return an artifact rendered with other
	// subtitles.
	cached, hit, err := r.reuseCache.Lookup(ctx, RenderReuseKey(plan, ass))
	if err != nil {
		// Fail-soft: an unavailable or erroneous cache is a miss, never a
		// failed render. The render below is the authoritative path.
		recordReuseMiss("lookup_error")
		return LocalizedClipArtifact{}, false
	}
	if !hit || cached == nil {
		recordReuseMiss("no_entry")
		return LocalizedClipArtifact{}, false
	}
	if reason := r.ensureReusableBytes(ctx, cached); reason != "" {
		recordReuseMiss(reason)
		return LocalizedClipArtifact{}, false
	}
	recordReuseHit()
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
		Reused:        true,
		RenderSource:  RenderSourceDeterministicCache,
		StorageKey:    cached.StorageKey,
		ArtifactURL:   cached.ArtifactURL,
		DriveFileID:   cached.DriveFileID,
		DriveLink:     cached.DriveLink,
		DriveFolderID: cached.DriveFolderID,
	}
	if ass != nil {
		artifact.SubtitlePath = ass.LocalPath
		artifact.SubtitleSHA256 = ass.SHA256
	}
	return artifact, true
}

// ensureReusableBytes makes a cache row usable, returning "" on success or the
// metric reason it could not. When the recorded local file still hashes to the
// certified digest the row is used as-is (the common case). Otherwise, if the
// row carries a durable ArtifactURL and the render boundary can hydrate it, the
// object is materialized under the reuse directory, verified again from the
// bytes on disk, and only then accepted. Anything short of that is a MISS.
func (r *LocalizedClipRenderer) ensureReusableBytes(ctx context.Context, cached *ReusedRenderArtifact) string {
	verifyStart := time.Now()
	verified := reusableBytes(cached)
	LocalizedRenderReuseVerifySeconds.Observe(time.Since(verifyStart).Seconds())
	if verified {
		return ""
	}
	if cached == nil || strings.TrimSpace(cached.ArtifactURL) == "" || strings.TrimSpace(r.reuseDir) == "" {
		return "unverifiable_bytes"
	}
	hydrator, ok := r.executor.(RenderReuseHydrator)
	if !ok {
		return "unverifiable_bytes"
	}
	if err := os.MkdirAll(r.reuseDir, 0o755); err != nil {
		return "hydrate_failed"
	}
	// Content-addressed filename: the same fingerprint reuses the same local
	// file on the next run, and concurrent hydrations of identical bytes race
	// only on an atomic rename.
	destPath := filepath.Join(r.reuseDir, digest.SHA256String(cached.Fingerprint)+".mp4")
	tmp, err := os.CreateTemp(r.reuseDir, "reuse-*.mp4.part")
	if err != nil {
		return "hydrate_failed"
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath)
	hydrateStart := time.Now()
	if err := hydrator.HydrateReusedRender(ctx, cached, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		LocalizedRenderReuseVerifySeconds.Observe(time.Since(hydrateStart).Seconds())
		return "hydrate_failed"
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return "hydrate_failed"
	}
	cached.LocalPath = destPath
	verifyStart = time.Now()
	verified = reusableBytes(cached)
	LocalizedRenderReuseVerifySeconds.Observe(time.Since(verifyStart).Seconds())
	if !verified {
		return "hydrate_failed"
	}
	// Remember the local materialization so the next hit on this host reads it
	// instead of downloading the same bytes again. Best-effort: the artifact is
	// already certified, so a failed write must not turn a hit into a miss.
	_ = r.reuseCache.Store(ctx, *cached)
	return ""
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
func (r *LocalizedClipRenderer) storeRendered(ctx context.Context, plan LocalizedClipPlan, ass *SubtitleAsset, facts RenderFacts) {
	if r == nil || r.reuseCache == nil {
		return
	}
	_ = r.reuseCache.Store(ctx, ReusedRenderArtifact{
		Fingerprint: RenderReuseKey(plan, ass),
		LocalPath:   facts.LocalPath,
		SHA256:      facts.SHA256,
		SizeBytes:   facts.SizeBytes,
		DurationMS:  facts.DurationMS,
		VideoCodec:  facts.VideoCodec,
		AudioCodec:  facts.AudioCodec,
		Backend:     facts.Backend,
		StorageKey:  facts.StorageKey,
		ArtifactURL: facts.ArtifactURL,
	})
}
