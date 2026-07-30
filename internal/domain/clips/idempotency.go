// Package clips — idempotency.go (Stock Pipeline Cutover P0-CLIP-IDEMP, July 2026).
//
// godlike/06 SSOT: this package is the SOLE canonical owner of
// "given a clip's deterministic identity (ClipKey), tell me
// where it already exists and record where I just put it". All
// other layers (use cases, infrastructure adapters, application
// handlers, the P0-2 stockbuild.Handler phase bodies) MUST
// consume the typed Idempotency port from THIS file rather than
// re-implementing the 4-case detector with a parallel
// `repo.Exists(clipID) bool + drive.Exists + qdrant.Scroll`
// triplet. Drift between an ad-hoc producer and this canonical
// port is the canonical godlike/06 SSOT violation — the entire
// "each run idempotent" guarantee from the user spec depends on
// ONE owner of "is the clip there?" semantics.
//
// godlike/07 NO-FAKE-AVAILABILITY: every method rejects empty
// inputs (clip_key, asset_id, drive_file_id, qdrant_point_id)
// with typed sentinels. The dedup guarantees on
// clip_storage_index.UNIQUE(clip_key) and on the presence-bit
// flip-1 transitions depend on inputs being COMPLETE.
//
// # 8 storage cases per user spec
//
//	┌─────┬───────┬─────────┬──────────┬──────────────────────────────────┐
//	│ #   │ hasDB │ hasDrv  │ hasQdr   │ orchestrator action              │
//	├─────┼───────┼─────────┼──────────┼──────────────────────────────────┤
//	│ 0   │   0   │   0     │   0      │ CREATE all 3 (full happy path)   │
//	│ 1   │   1   │   0     │   0      │ repair UPLOAD + repair INDEX     │
//	│ 2   │   0   │   1     │   0      │ repair PERSIST (no Drive reupload)│
//	│ 3   │   0   │   0     │   1      │ ErrStorageInconsistent (orphan)  │
//	│ 4   │   1   │   1     │   0      │ repair INDEX (emit outbox)       │
//	│ 5   │   1   │   0     │   1      │ repair UPLOAD (DB ← Qdrant ok)   │
//	│ 6   │   0   │   1     │   1      │ ErrStorageInconsistent (orphan)  │
//	│ 7   │   1   │   1     │   1      │ SKIP — fully done                │
//	└─────┴───────┴─────────┴──────────┴──────────────────────────────────┘
//
// Cases 3 and 6 are TYPED ERRORS (ErrStorageInconsistent) — a
// Qdrant/Drive point without a media_assets row has no
// salvageable asset_id (the DB IS the asset_id carrier per
// godlike/06), so the clip cannot be linked back to its row;
// an operator's diagnostic distresses this exact failure mode.
//
// # Integration with P0-2 stockbuild.Handler
//
// Each phase body consumes Inspect() + RecordX() to decide
// skip vs repair. The 8-phase machine becomes idempotent by
// construction — a crashed mid-flight run resumes from the
// first non-Completed phase via steps.Store (Stock Cutover
// §12-3), and each resumed phase queries Inspect() so the
// repair half of the gap closes the unfinished work.
//
// Composition root wiring lives in
// internal/app/wiring/build_bundles_clips_idempotency.go
// (the equivalent of build_bundles_subjects.go from P0-1).
package clips

import (
	"context"
	"errors"
	"fmt"
)

// LayerPresence is the per-clip per-layer gate state. The 3
// fields are INDEPENDENT — there is no "HasDB implies might
// have Drive" inference. The clip_key (sha256 of subject|
// video|start_ms|end_ms) is the linking key for repair, NOT
// a derived fact from a single column on media_assets.
//
// The struct is a value type (no pointer indirection) so the
// SQLite adapter can `return clips.LayerPresence{...}, nil`
// cheaply; per-layer flags are individual bool fields instead
// of a bitmask because godlike/07 typed-error readability is
// higher with explicit field access (HasDB vs `(presence>>0)&1`).
type LayerPresence struct {
	HasDB     bool // media_assets row exists for the clip_key
	HasDrive  bool // Drive file uploaded (drive_file_id materialised)
	HasQdrant bool // IndexingHandler emitted asset.index.requested → Qdrant point created
}

// AnythingAbsent reports whether NO layer has the clip yet
// (case 0 in the matrix above). Pure function so callers
// (clip-create use case, the run-reconciler) can branch on
// "absent everywhere" without inlining the negation each time.
func (lp LayerPresence) AnythingAbsent() bool {
	return !lp.HasDB && !lp.HasDrive && !lp.HasQdrant
}

// FullyPresent reports whether ALL three layers have the clip
// (case 7 in the matrix above). This is the canonical skip-on-
// completion state that the orchestrator hits on a no-op resume.
func (lp LayerPresence) FullyPresent() bool {
	return lp.HasDB && lp.HasDrive && lp.HasQdrant
}

// Inconsistent reports the cases that cannot be repaired
// without operator action (cases 3 and 6 — Drive or Qdrant
// present without the SQLite row that anchors asset_id). The
// caller MUST treat Inconsistent() == true as an error per
// godlike/07 (fail-closed, typed error surfaced to the operator).
func (lp LayerPresence) Inconsistent() bool {
	return !lp.HasDB && (lp.HasDrive || lp.HasQdrant)
}

// Idempotency is the typed port for clip-storage-layer
// presence tracking and recording. Application use cases
// depend on this interface; infra adapters (SQLite) implement
// it; composition root wires them.
//
// godlike/06: this is the SOLE canonical surface for
// clip-storage-layer queries. Do NOT introduce a parallel
// `clipExists(clipID) bool` port — that would silently bypass
// the 4-case detector and risk a re-run that double-writes
// a clip already stamped in clip_storage_index.
//
// Method semantics:
//   - Inspect: read-only presence query. Returns
//     (LayerPresence{all-false}, nil) for unseen clip_key
//     (the canonical "fresh clip" state).
//   - RecordPersistence: flip has_db 0→1 on first call, stamp
//     persisted_at + asset_id. Idempotent on repeated calls
//     with same (clipKey, assetID).
//   - RecordDrive: flip has_drive 0→1, stamp uploaded_at +
//     drive_file_id + drive_link. Idempotent.
//   - RecordQdrant: flip has_qdrant 0→1, stamp indexed_at +
//     qdrant_point_id. Idempotent.
//
// Each Record method is the "after-the-fact side-effect
// marker" — they NEVER do the actual DB/Drive/Qdrant write.
// The orchestrator's phase body executes the underlying write
// first (e.g. via PersistClipAndIndex, PublishClipToDrive,
// outbox.asset.index.requested emission), then calls Record
// to update the presence ledger. This ordering matches the
// user-spec literal "write → record". The Record methods are
// there to observe, not to write.
type Idempotency interface {
	Inspect(ctx context.Context, clipKey string) (LayerPresence, error)
	RecordPersistence(ctx context.Context, clipKey, assetID string) error
	RecordDrive(ctx context.Context, clipKey, driveFileID, driveLink string) error
	RecordQdrant(ctx context.Context, clipKey, qdrantPointID string) error
}

// Typed sentinels for fail-closed guards. godlike/07:
// callers branch on errors.Is to surface a typed diagnostic.
// All sentinels carry the canonical "no fake availability"
// reason text for grep-ability.
var (
	// ErrEmptyClipIdentity — every Idempotency method
	// rejects an empty clip_key. The clip_storage_index
	// UNIQUE keeps the row identity stable; a half-keyed
	// insert would silently collide on a phantom row.
	ErrEmptyClipIdentity = errors.New("clips.Idempotency: clip_key is required (godlike/07 - no fake availability)")

	// ErrEmptyAssetID — RecordPersistence rejects an empty
	// asset_id. The asset_id is the link to media_assets;
	// a missing asset_id means the INDEX phase has no
	// row to emit asset.index.requested against (the
	// outbox needs the media_assets.id target).
	ErrEmptyAssetID = errors.New("clips.Idempotency: asset_id is required for RecordPersistence (godlike/07 - no fake availability)")

	// ErrEmptyDriveFileID — RecordDrive rejects an empty
	// drive_file_id. The has_drive=1 flip without a
	// materialised drive_file_id means a downstream
	// operator cannot reconcile the row against Drive's
	// actual folder listing.
	ErrEmptyDriveFileID = errors.New("clips.Idempotency: drive_file_id is required for RecordDrive (godlike/07 - no fake availability)")

	// ErrEmptyQdrantPointID — RecordQdrant rejects an empty
	// qdrant_point_id. The has_qdrant=1 flip without a
	// materialised point_id means a downstream operator
	// cannot reconcile against Qdrant's actual collection.
	ErrEmptyQdrantPointID = errors.New("clips.Idempotency: qdrant_point_id is required for RecordQdrant (godlike/07 - no fake availability)")

	// ErrStorageInconsistent — semantic sentinel for the
	// "Qdrant or Drive present without media_assets row"
	// cases (3 and 6 in the matrix). The read path cannot
	// derive the asset_id from clip_key alone — the SQLite
	// row IS the asset_id container; an orphan point/file
	// has no salvageable asset_id.
	//
	// godlike/06 contract: this is the ONLY failure class
	// where the orchestrator does NOT repair and the
	// operator MUST intervene. Operators grep on
	// `clips.ErrStorageInconsistent` to find orphan
	// storage artefacts.
	ErrStorageInconsistent = errors.New("clips.Idempotency: storage state inconsistent - Drive or Qdrant present without media_assets row (DB is source of truth per godlike/06; the failing clip_key has no salvageable asset_id)")
)

// ─── Composition-root guard ──────────────────────────────────────────────────

// MustNewIdempotency is the canonical factory called from
// internal/app/wiring. It panics on nil so a half-wired boot
// fails loud at startup with a typed reason (godlike/07); the
// recoverable variant is the underlying constructor (infra).
// Callers should never import the panic version outside the
// composition root.
func MustNewIdempotency(impl Idempotency) Idempotency {
	if impl == nil {
		panic(fmt.Errorf("clips.MustNewIdempotency: Idempotency port is required (godlike/07 - no fake availability)"))
	}
	return impl
}
