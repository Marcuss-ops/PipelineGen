// Package staged — resolver.go (Azione 1, July 2026,
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave).
//
// The staged package owns ONE canonical fact, per godlike/06 SSOT
// discipline:
//
//	"Resolve an abstract artifactID to a concrete StagedArtifact
//	 describing the local path + SHA + size on the Sender box
//	 (worker-side local filesystem), via DB lookup in asset_index."
//
// godlike/06 SSOT rationale: a single canonical owner of the
// lookup-result identity contract. The pre-existing StagedAsset type
// (in internal/application/assets/ports.go, Step 9/12 SourceStager
// wave) owns a DIFFERENT fact ("freshly staged bytes on disk from a
// URL"); this new StagedArtifact owns "stable lookup result for an
// existing artifact's local path + SHA". The two types coexist per
// godlike/06 "one owner per fact": they document DIFFERENT invocations
// of the disk state.
//
// godlike/07 typed-error contract: every failure path returns a typed
// sentinel reachable via errors.Is (see errors.go). The 3-step pipeline
// (DB lookup + os.Stat + SHA recompute) emits one of three typed
// sentinels on fail-closed paths. There is no zero-value fallback path
// per godlike/07 §"No fake availability".
//
// Idempotency contract: two successive ResolveStagedArtifact(same artifactID) calls
// return IDENTICAL *StagedArtifact shapes (sans pointer identity):
//   - Path    : deterministic from DB lookup (stable across calls).
//   - SHA256  : deterministic from on-disk bytes (recomputed every call
//     via os.Open + io.Copy + digest.SHA256Bytes — NEVER cached).
//   - Bytes   : deterministic from os.Stat.Size() (recomputed every
//     call — NEVER cached).
//   - Source  : deterministic from DB lookup (stable across calls).
//
// Pattern 0 (AGENTS.md godlike/06 / §"Port abstraction layer"): the
// resolver exposes a typed port (StagedArtifactResolver, single-method)
// and accepts a typed-fingerprint lookup seam (ArtifactIndexLookupFn) for
// the DB dependency. Composition root wires the production concrete +
// real lookupFn; tests pass stub lookupFn. The compile-time pin (line
// `var _ StagedArtifactResolver = (*Resolver)(nil)`) catches future drift
// at build time, not at the first call site.
package staged

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── StagedArtifact typed envelope ──────────────────────────────────

// StagedArtifact is the stable lookup result for an existing artifact's
// local path + SHA on the Sender box. All fields are recomputed or
// look-up-fetched at Resolve-time so two calls with same artifactID are
// idempotent (identical shape, possibly different *StagedArtifact pointer
// identity).
//
// godlike/06 SSOT discipline: distinct from the pre-existing StagedAsset
// in ports.go (Step 9/12 SourceStager) — that one owns "freshly staged
// bytes from a URL"; this StagedArtifact owns "stable lookup-result
// identity for an existing artifact". One owner per fact, two types
// coexist by design (mirrors the canonical StagedAsset / VerifiedArtifact
// / PublishedArtifact chain across Azioni 1-3).
type StagedArtifact struct {
	// AssetID is the input artifact_id; preserved on the result envelope
	// so downstream call sites can correlate without re-passing it.
	AssetID string
	// Path is the local filesystem path on the Sender box. Source: DB
	// lookup via the lookupFn lambda; stable across Resolve calls.
	// Canonical alias: LocalPath carries the same value (same byte-stream
	// read from the same DB row); callers may dereference either name.
	Path string
	// LocalPath is the canonical-typed alias of Path (the
	// verification.Verifier contract reads sa.LocalPath — see verification/
	// verified.go). Populated from row.Path at Resolve time. Identical
	// byte-stream to Path; future golangci-lint rule may collapse the
	// short/long pair once verified.go is migrated.
	LocalPath string
	// SHA256 is the hex-encoded SHA-256 hash of the file at Path. Source:
	// on-disk recompute via os.Open + io.Copy + digest.SHA256Bytes. Recomputed
	// every call (NOT cached) per the user's idempotency spec.
	SHA256 string
	// Bytes is the file size at Path. Source: os.Stat.Size(). Recomputed
	// every call (NOT cached) per the user's idempotency spec. Canonical
	// alias: SizeBytes carries the same value.
	Bytes int64
	// SizeBytes is the canonical-typed alias of Bytes (the
	// verification.Verifier contract reads sa.SizeBytes — see
	// verification/verified.go). Populated from info.Size() at Resolve
	// time. Identical byte-stream to Bytes.
	SizeBytes int64
	// Source is the originating provider label (e.g. "artlist",
	// "voiceover", "images", "stock"). Source: DB lookup. Stable across
	// calls.
	Source string
}

// ── Port + lookup seam (Pattern 0) ──────────────────────────────────

// StagedArtifactResolver is the canonical Pattern 0 port entry-point.
// Composition root wires the production *Resolver; tests mock via the
// interface. Single-method because the resolver does ONE thing per
// godlike/06 SSOT discipline — adding more methods is a deliberate
// contract change (mirrors the canonical SourceRepo /
// EnrichStateMachinePort shape used elsewhere in the codebase).
type StagedArtifactResolver interface {
	// ResolveStagedArtifact returns the canonical 5-field *StagedArtifact
	// envelope for the given artifactID via the lookupFn → os.Stat →
	// SHA-256 recompute pipeline. The method name matches the user spec
	// verbatim (AGENTS.md §"Code Hygiene": exported names referenced in
	// documentation must match the implementation).
	ResolveStagedArtifact(ctx context.Context, artifactID string) (*StagedArtifact, error)
}

// ArtifactIndexLookupFn is the typed-fingerprint lookup seam to the DB.
// Production wires against a concrete ClipsRepository or future
// assetindex_repository call; tests pass a stub that returns *IndexRow
// + error. Returns ErrStagedArtifactMissing wrapped via fmt.Errorf %w
// when the DB has no row for artifactID — preserves the typed-error
// chain.
//
// The canonical DB schema lives in migrations/sqlite/001_velox_core.sql
// (asset_index: asset_id PRIMARY KEY, asset_type, source, source_id,
// operation_key, group_name, subfolder, local_path, drive_link,
// download_link, file_hash, content_hash, status, metadata_json,
// created_at, updated_at). Forward-pointer: migration 121 (Azione 15)
// may add a `staged_at` column for the StagedAsset → StagedArtifact
// evolution; until then, status='staged' (Azione 15 introduced) marks
// the canonical pre-publish surface.
//
// godlike/06 SSOT: this lambda is the ONLY canonical seam between the
// resolver and the storage layer. Any future change to the underlying
// table (column rename, schema evolution) surfaces HERE as a typed
// envelope, never via a wire-leaked raw string.
type ArtifactIndexLookupFn func(ctx context.Context, artifactID string) (*IndexRow, error)

// IndexRow carries the lookup result from the storage layer. The
// SHA256 stored-at-staging-time field is preserved for diagnostic
// sanity-checks; the canonical Resolver.Resolve recomputes SHA from
// disk anyway (NOT propagated to *StagedArtifact).
type IndexRow struct {
	// Path is the local filesystem path on the Sender box.
	Path string
	// Source is the originating provider label (e.g. "artlist").
	Source string
	// SHA256 stored at staging time. Optional diagnostic; Resolver.Resolve
	// does not propagate it (it recomputes live).
	SHA256 string
}

// ── Concrete Resolver ──────────────────────────────────────────────

// Resolver is the concrete implementation of StagedArtifactResolver.
// Thread-safety: stateless post-construction; safe for concurrent use
// from multiple goroutines (the lookupFn is the only shared outer-state
// and is expected to be concurrency-safe per Pattern 0 production wiring).
type Resolver struct {
	lookup ArtifactIndexLookupFn
}

// NewResolver is the canonical constructor. Fail-closed per godlike/07:
// returns ErrStagedArtifactNotConfigured when lookup is nil so a
// half-wired composition surfaces the failure at startup rather than
// at the first Resolve call (mirrors the canonical fail-closed posture
// of P0-Commit 7's NewService constructor in the completion package).
func NewResolver(lookup ArtifactIndexLookupFn) (*Resolver, error) {
	if lookup == nil {
		return nil, ErrStagedArtifactNotConfigured
	}
	return &Resolver{lookup: lookup}, nil
}

// Compile-time pin (Pattern 0): catastrophic drift between the
// StagedArtifactResolver port and the *Resolver concrete is a build
// failure, not a runtime panic. Future drift (e.g. adding a method to
// the port) MUST update *Resolver; this assertion surfaces the
// regression at build time, not at the first call site.
var _ StagedArtifactResolver = (*Resolver)(nil)

// ── Resolve (3-step pipeline) ──────────────────────────────────────

// ResolveStagedArtifact executes the canonical 3-step pipeline:
//
//	(1) DB lookup via lookupFn        — emits ErrStagedArtifactMissing
//	                                   on miss or empty Path.
//
//	(2) os.Stat for file existence    — emits ErrStagedArtifactNotOnDisk
//	                                   when the row points to an absent
//	                                   local file (godlike/07 tripwire).
//
//	(3) SHA-256 live recompute        — recomputes via os.Open + io.Copy
//	                                   + digest.SHA256Bytes every call (NEVER
//	                                   cached).
//
// Step (3) is the user-spec idempotency anchor: SHA + Bytes are
// recomputed at lookup time, NEVER cached, so two successive calls
// with the same artifactID return identically-shaped *StagedArtifact
// values (only the pointer identity might differ).
//
// Nil-receiver guard: a nil *Resolver returns ErrStagedArtifactNotConfigured
// (godlike/07 fail-closed posture; the failure mode is operator-visible,
// not a panic). Nil-empty artifactID returns ErrStagedArtifactMissing
// wrapped via %w (no fake availability: zero-value Path/SHA is not a
// valid substitute).
func (r *Resolver) ResolveStagedArtifact(ctx context.Context, artifactID string) (*StagedArtifact, error) {
	if r == nil {
		return nil, ErrStagedArtifactNotConfigured
	}
	if artifactID == "" {
		return nil, fmt.Errorf("%w: artifactID is empty", ErrStagedArtifactMissing)
	}

	// (1) DB lookup. The lookupFn is expected to return
	// ErrStagedArtifactMissing wrapped via fmt.Errorf %w when the row
	// is absent — propagate the chain via errors.Is so callers reach
	// the typed sentinel.
	row, err := r.lookup(ctx, artifactID)
	if err != nil {
		if errors.Is(err, ErrStagedArtifactMissing) {
			return nil, err
		}
		return nil, fmt.Errorf("staged.ResolveStagedArtifact(%q): db lookup: %w", artifactID, err)
	}
	if row == nil || row.Path == "" {
		// DB row present but Path is empty: convert to ErrStagedArtifactMissing.
		// godlike/07 row-nil guard: a "row without path" is a corrupted row and
		// MUST throw the typed sentinel, not the zero-value envelope.
		return nil, fmt.Errorf("%w: row present but path empty (artifactID=%q)",
			ErrStagedArtifactMissing, artifactID)
	}

	// (2) os.Stat for existence + file size.
	info, err := os.Stat(row.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: row.Path=%q (artifactID=%q)",
				ErrStagedArtifactNotOnDisk, row.Path, artifactID)
		}
		return nil, fmt.Errorf("staged.ResolveStagedArtifact(%q): stat row.Path=%q: %w",
			artifactID, row.Path, err)
	}

	// (3) SHA-256 live recompute. Streaming read via io.Copy with a
	// tee-reader to sha256 handles arbitrarily-large files without
	// loading the whole file into memory. The canonical test surface
	// (Azione 8 E2E test) uses sub-MB PNG fixtures, so the per-read
	// overhead is negligible at production scale.
	sha256Hex, err := recomputeSHA256(row.Path)
	if err != nil {
		return nil, fmt.Errorf("staged.ResolveStagedArtifact(%q): recompute sha256 row.Path=%q: %w",
			artifactID, row.Path, err)
	}

	return &StagedArtifact{
		AssetID:   artifactID,
		Path:      row.Path,
		LocalPath: row.Path,
		SHA256:    sha256Hex,
		Bytes:     info.Size(),
		SizeBytes: info.Size(),
		Source:    row.Source,
	}, nil
}

// recomputeSHA256 streams the file through digest.SHA256Bytes and returns the
// hex-encoded digest. io.Copy with a hash destination handles arbitrarily-
// large files (canonical production asset sizes range from KB PNGs to
// GB-scale videos; streaming avoids peak-memory pressure on large
// artifacts).
func recomputeSHA256(path string) (string, error) {
	h, _, err := digest.SHA256File(path)
	if err != nil {
		return "", fmt.Errorf("digest: %w", err)
	}
	return h, nil
}
