// Package voiceover — narrow port interfaces for out-of-package
// dependencies (AGENTS.md Pattern 0, June 2026).
//
// Two ports live here:
//
//  1. TxOutboxEnqueuer — PR-VO-A3 (June 2026, outbox-based Qdrant
//     indexing). The canonical `asset.index.requested` enqueue site
//     moved INTO swapVoiceoverRow's SQLite transaction. The metadata
//     UPSERT (voiceovers row) and the outbox event INSERT
//     (asset.index.requested) now commit atomically — no orphan
//     events, no orphan embeddings, no async goroutine race.
//
//  2. DriveUploaderPort — PR-VO-B1 (June 2026, Drive upload split).
//     voiceover.Service now reaches Drive only through this narrowed
//     port. Processor writes local FS only; Lifecycle owns the upload;
//     voiceover uses the port exclusively for the post-commit cleanup
//     DeleteFile.
//
// Layout note: voiceover never imports the SDK. Production concretes
// satisfy these ports by structural conformance (Go's implicit
// interface rules). Tests substitute stubs that record invocations.
package voiceover

import (
	"context"
	"database/sql"
)

// TxOutboxEnqueuer is the canonical narrow port for transactional
// outbox enqueue from inside the voiceover-swap transaction.
//
// Calling sites MUST pass a caller-owned *sql.Tx so the producer
// commit collapses both the voiceovers INSERT and the indexing event
// INSERT into a single atomic visibility boundary. A nil implementation
// is allowed at construction time (ProcessAsset guards nil at the
// call site so the optional behaviour degrades to "skip indexing" —
// same pattern as the previous ClipIndexFunc callback).
type TxOutboxEnqueuer interface {
	// EnqueueIndexEvent emits the canonical asset.index.requested
	// envelope (schema_version="asset.index.requested.v1") inside
	// the caller-owned transaction.
	//
	// assetID — the voiceover ID (voiceovers.id), used as the
	// canonical aggregate identifier. Matches the convention used
	// by outbox.Dispatcher.EnqueueAndIndex (whose assetID is the
	// media_assets.id).
	//
	// contentHash — the content fingerprint (typically the file MD5)
	// used for the supersede gate (the worker's source_version
	// check) and event_key derivation (idempotency). MUST be
	// non-empty; the canonical dispatcher rejects empty hashes
	// because the supersede gate cannot function without them.
	EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, contentHash string) error
}

// DriveUploaderPort is the narrow Drive surface the voiceover service
// uses directly. PR-VO-B1 (June 2026): voiceover previously held a
// *drive.Uploader field; the constructor now takes this port. The
// production concrete is *drive.Uploader (wrapped by
// app/voiceoverDriveAdapter because the canonical layering rule
// forbids infrastructure/drive from importing
// internal/application/voiceover).
//
// Today's single exposure is DeleteFile: swapVoiceoverRow's
// post-commit cleanup goroutine (processLanguage's
// replace-mode orphan eviction) routes an OLD voiceover's Drive
// file through this port. Adding new methods is permitted but
// should follow the same narrow-target practice — one method per
// Drive operation the voiceover service actually calls, no
// pre-emptive methods.
//
// Behavior shift note (PR-VO-B1): the previous audioasset.Processor
// ran an inline Drive upload with **log-warn best-effort** semantics
// (failure swallowed, status remained "generated"/"cleaned"). The
// new design routes the upload through Lifecycle.ProcessAsset
// (Step 2 in internal/application/assets/lifecycle/service.go)
// which **fails-fast** with `lifecycle_failed`. This is an
// intentional hardening of the obsolete silent-upload path — the
// caller now sees Drive upload failures instead of an orphan file
// that the rest of the pipeline silently passed.
//
// Nil-safe: processLanguage guards nil at the call site and
// short-circuits the cleanup goroutine. The production wiring in
// internal/app/build_bundles_voiceover.go always supplies a
// non-nil adapter.
type DriveUploaderPort interface {
	DeleteFile(ctx context.Context, fileID string) error
}

// VoiceoverGenerator is the BACKFILL typed-port (Wave 21
// PR-VOICEOVER-TYPED-PORT-RECOVERY-PHASE2, B-2 step closure, per
// AGENTS.md Pattern 0 — port abstraction layer, June 2026).
//
// Shape: matches main's *Service.Generate signature exactly (positional
// ctx + text + language + filename). The original blueprint called for
// a `voiceover.GenerateVoiceoverCommand` struct, but the live main shape
// is positional — per the user's re-execution directive: "usando il type
// del dominio attuale di main, NON blueprint".
//
// Back-compat note: the legacy *Service satisfies this port
// structurally via Go's implicit-interface rule. Test doubles inject
// stubs via ServiceDeps in usecase.go (no production behavior change).
//
// The VoiceoverResult return type is the package-local struct declared
// in types.go (NOT the domain.voiceover.VoiceoverResult alias for the
// canonical Result — those are intentionally separate types: the
// application struct is the wire-shape for Service.Generate, the
// domain Result is the canonical full-typed version used by other
// Wave 21 PR-G artefacts).
type VoiceoverGenerator interface {
	Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error)
}

// Compile-time assertion (AGENTS.md Pattern 0): *Service must
// structurally satisfy VoiceoverGenerator. Drift between Service.Generate
// signature and the port contract triggers a compile error at this
// line — preventing silent drift on the wire contract.
var _ VoiceoverGenerator = (*Service)(nil)
