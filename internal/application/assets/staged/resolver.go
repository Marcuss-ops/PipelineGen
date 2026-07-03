// Package staged — resolver.go: Azione 3 of the
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave. Resolves an AssetID into
// the canonical StagedArtifact envelope that the cutover pipeline
// hands off to ArtifactPreparation / Drive Publisher (Azione 5).
//
// Idempotency is achieved by deterministic-by-construction: every
// invocation re-reads the live file at the looked-up local path
// and re-derives the SHA-256 from the read bytes. Two consecutive
// calls with the same AssetID return byte-equivalent StagedArtifacts
// (assuming the file hasn't been mutated between calls).
//
// Note on idempotency vs drift-detection: the resolver's "recompute
// on every call" choice IS the idempotency mechanism AND the
// silent-corruption-detector. If the file at the looked-up path
// is mutated between calls (TTL GC sweep, parallel write, fs
// corruption), the two calls return DIFFERENT StagedArtifacts
// — which the cutover pipeline treats as a "stale" event and
// either re-fetches or fails closed. Per godlike/07 no-fake-
// availability: never returns a struct with stale crypto state.
package staged

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// StagedArtifact is the canonical envelope returned by the
// Resolver. The struct is dedicated to the COMPLETE-side staged
// lifecycle on the Sender (post remote-worker upload, pre Drive
// publish) — it is NOT the same as the download-side
// SourceStager.StagedAsset in internal/application/assets/ports.go.
//
// Splitting the two structs mirrors the 2-stage worker→sender→drive
// protocol: one struct per staged state, per godlike/06 one-owner-
// per-fact SSOT discipline. Extending the existing SourceStager
// struct with a SHA256 field would conflate the two state machines
// and ripple the SHA recompute contract onto the YouTube / Stock /
// Artlist adapter tree (each of which already returns its own
// shape with intentionally benign SHA semantics).
//
// Fields:
//   - AssetID: the canonical ID, byte-stable across retries
//     (asset_index.asset_id PRIMARY KEY).
//   - LocalPath: absolute filesystem path on the Sender. Re-derived
//     from the IndexStore on every call (no in-process memoization,
//     per godlike/07).
//   - SHA256: hex-encoded SHA-256 digest of the file content at
//     LocalPath. Re-computed on every call from the LIVE bytes
//     (never read from asset_index.content_hash, which is treated
//     as decorative metadata only).
//   - SizeBytes: os.Stat size of the file at LocalPath — the
//     authoritative caller-side payload size for the downstream
//     ArtifactPreparation.Prepare step (Azione 5).
type StagedArtifact struct {
	AssetID   string
	LocalPath string
	SHA256    string
	SizeBytes int64
}

// IndexStore is the narrow typed port (Pattern 0 godlike/06 SSOT)
// the Resolver depends on. The composition root injects the
// production binding (an adapter over
// internal/infrastructure/database/assetindex.Repository); the
// tests inject hand-rolled stubs via resolver_test.go.
//
// GetLocalPath returns the canonical staged local path for the
// canonical assetID, or ("", nil) when no row exists in
// asset_index. The Resolver treats an empty returned path AS the
// not-found signal regardless of the error value to keep the
// cutover contract branch-consistent (one errors.Is probe
// covers both DB-miss and DB-error).
//
// The signature deliberately does not leak the asset_index
// AssetRecord shape: the only field the Resolver needs is
// local_path, so the port is 1-field narrow. Sibling ports in
// the cutover wave (e.g. for the VerifiedArtifact projection in
// Azione 4) declare their own field-narrow interfaces; per
// godlike/06 "one owner per fact" the Resolver Port owns ONLY
// local_path lookups.
type IndexStore interface {
	GetLocalPath(ctx context.Context, assetID string) (string, error)
}

// Resolver resolves asset_index rows into canonical StagedArtifact
// envelopes. Stateless + deterministic. See package doc comment
// for the idempotency + drift-detection contract.
type Resolver struct {
	store IndexStore
}

// NewResolver constructs a Resolver over the supplied IndexStore.
// Per Pattern 0 godlike/06 SSOT and godlike/05 wiring-error rule,
// this constructor returns an explicit error when store is nil
// — the composition root must surface the missing mandatory dep
// at startup rather than deferring to a runtime NPE on first
// resolver request.
func NewResolver(store IndexStore) (*Resolver, error) {
	if store == nil {
		return nil, fmt.Errorf("staged.NewResolver: IndexStore is required (composition root must wire the assetindex.Repository adapter)")
	}
	return &Resolver{store: store}, nil
}

// ResolveStagedArtifact is the canonical entry point per Azione 3
// of the CUTOVER-COMPLETE-WITH-ARTIFACTS wave.
//
// Pipeline (3-step verification chain):
//
//   1. IndexStore.GetLocalPath(ctx, artifactID) — DB lookup. Empty
//      returned path OR non-nil error → wrapped ErrStagedArtifactMissing.
//   2. os.Stat(localPath) — file existence + size. Stat failure
//      (file deleted/moved/TTL'd) → wrapped ErrStagedArtifactMissing.
//   3. files.HashFile(localPath, sha256.New()) — live SHA-256
//      recompute. NEVER falls back to asset_index.content_hash
//      (godlike/07 no-fake-availability: stale DB hash would
//      silently publish a corrupted file). Hash failure → wrapped
//      ErrStagedArtifactMissing.
//
// On success, returns a populated StagedArtifact (AssetID +
// LocalPath + SHA256 + SizeBytes).
//
// Per godlike/07 no-fake-availability: the function returns
// error early on EVERY failure path, never returns a struct with
// empty path / empty hash / 0 size. The cutover pipeline upstream
// branches on errors.Is(err, staged.ErrStagedArtifactMissing)
// exactly once and drops the artifact — no further typed-error
// discrimination needed.
func (r *Resolver) ResolveStagedArtifact(ctx context.Context, artifactID string) (*StagedArtifact, error) {
	localPath, err := r.store.GetLocalPath(ctx, artifactID)
	if err != nil {
		return nil, fmt.Errorf("staged.ResolveStagedArtifact[%s]: index lookup: %w", artifactID, err)
	}
	if localPath == "" {
		return nil, fmt.Errorf("staged.ResolveStagedArtifact[%s]: index lookup returned empty local path: %w", artifactID, ErrStagedArtifactMissing)
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("staged.ResolveStagedArtifact[%s]: stat failed for %q: %w", artifactID, localPath, ErrStagedArtifactMissing)
	}

	hashHex, err := files.HashFile(localPath, sha256.New())
	if err != nil {
		return nil, fmt.Errorf("staged.ResolveStagedArtifact[%s]: sha256 recompute failed for %q: %w", artifactID, localPath, ErrStagedArtifactMissing)
	}

	return &StagedArtifact{
		AssetID:   artifactID,
		LocalPath: localPath,
		SHA256:    hashHex,
		SizeBytes: info.Size(),
	}, nil
}
