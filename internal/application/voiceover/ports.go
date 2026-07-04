// Package voiceover — narrow port interfaces for out-of-package
// dependencies (AGENTS.md Pattern 0, June 2026).
//
// Fase 4 Spina Dorsale (July 2026): ports are organised into three
// territories matching the domain separation:
//
//   ┌─ VoiceoverSynthesis ───────────────────────────────────────┐
//   │  TTSProvider       — text→speech (NO database/sql, NO     │
//   │  AudioPostProcessor  drive, NO qdrant imports)              │
//   │  TTSInput / TTSOutput — application-layer wire shapes       │
//   └────────────────────────────────────────────────────────────┘
//   ┌─ VoiceoverPublication ────────────────────────────────────┐
//   │  VoiceoverPublisher — Drive upload via delivery.Publisher  │
//   │  VoiceoverPublishCommand — upload-only wire shape          │
//   │  CanonicalDriveWebURL / CanonicalDriveDownloadURL helpers  │
//   │  DestinationResolver — folder resolution (pre-upload)      │
//   │  VoiceoverDefaultFolderResolver — config-level fallback    │
//   └────────────────────────────────────────────────────────────┘
//   ┌─ VoiceoverFinalization ───────────────────────────────────┐
//   │  VoiceoverFinalizer — 6-step atomic commit (DB+lifecycle+ │
//   │  VoiceoverPostCommitVerifier  outbox) inside caller-owned tx│
//   │  TxOutboxEnqueuer — asset.index.requested outbox event     │
//   │  DriveUploaderPort — post-commit cleanup (DeleteFile only) │
//   │  VoiceoverItemExecutor — per-item pipeline orchestrator    │
//   └────────────────────────────────────────────────────────────┘
//
// The package-level `database/sql` import exists ONLY for
// Finalization-territory ports (VoiceoverFinalizer, TxOutboxEnqueuer,
// VoiceoverPostCommitVerifier). Synthesis-territory ports (TTSProvider,
// AudioPostProcessor) carry ZERO sql/drive/qdrant imports.
//
// Layout note: voiceover never imports the SDK. Production concretes
// satisfy these ports by structural conformance (Go's implicit
// interface rules). Tests substitute stubs that record invocations.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
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

	// EnqueueCleanupEvent emits the canonical
	// voiceover.cleanup.requested envelope inside the caller-owned
	// transaction. P0.7 Wave 21 Step 10/12 (June 2026) replaces
	// the pre-fix fire-and-forget `cleanupOrphanVoiceover`
	// goroutine (detached via context.Background) with this durable
	// outbox event. Producer (voiceover.finalizeStage) calls this
	// INSIDE the same SQL tx as the voiceovers UPSERT +
	// media_assets projection UPSERT, so the cleanup event commits
	// atomically with the canonical swap; a tx rollback discards
	// it (no orphan cleanup records survive a rolled-back
	// finalize).
	//
	// voiceoverID — the canonical voiceovers.id (also
	// media_assets.id — shared primary key).
	//
	// oldDriveFileID — the Drive file id of the row being replaced
	// (pre-swap). May be empty if no prior row existed.
	//
	// newDriveFileID — the Drive file id of the new row (post-swap).
	// The handler deletes oldDriveFileID ONLY when this differs
	// from it (i.e. a real swap happened; else no-op).
	//
	// oldLocalPaths — the local file paths of the OLD audio
	// (LocalPath + CleanedPath). The handler removes each; an
	// os.IsNotExist error is swallowed for idempotency.
	EnqueueCleanupEvent(ctx context.Context, tx *sql.Tx, voiceoverID, oldDriveFileID, newDriveFileID string, oldLocalPaths []string) error
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
	// GenerateWithDestination is the canonical narrow-port surface for
	// destination-aware voiceover generation. Production *Service and
	// any future implementations MUST satisfy this signature exactly; the
	// compile-time assertion below catches drift.
	//
	// Cross-capability cleanup Refactor 1 (June 2026, audit at
	// architecture/audits/2026-06-28-cross-capability-imports.md):
	// internal/application/scripts/jobs/job_helpers.go previously depended
	// on the concrete *voiceover.Service for this call site; the
	// dependency is now narrowed to this port-method, so the consumer
	// (scripts/jobs) compiles without importing the Service concrete
	// other than for the wire-shape *DestinationRequest / *VoiceoverResult
	// types returned.
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *DestinationRequest) (*VoiceoverResult, error)
}

// Compile-time assertion (AGENTS.md Pattern 0): *Service must
// structurally satisfy VoiceoverGenerator. Drift between Service.Generate
// signature and the port contract triggers a compile error at this
// line — preventing silent drift on the wire contract.
var _ VoiceoverGenerator = (*Service)(nil)

// ────────────────────────────────────────────────────────────────────────
// PR-VOICEOVER-COMMAND-EXTRACT (Blocco 2, June 2026): 7 canonical ports.
// ────────────────────────────────────────────────────────────────────────
//
// The new GenerateVoiceoversUseCase.Execute path depends ONLY on these
// ports (Pattern 0 — port abstraction layer, June 2026). Each port
// exposes a single, narrow method so test doubles can swap one port
// at a time without faking the wider surface.
//
// Layout note: the new ports are declared in this file (not split
// per-port) so the package's narrow port surface stays discoverable
// in one place. The package-level imports already cover context +
// database/sql; adding them here avoids dragging in any new dependency.
// The use case Execute enforces mandatory ports at construction time
// (panic on nil — fail-fast per AGENTS.md WireUp pattern).

// TTSProvider is the canonical port for text-to-speech synthesis.
// The production concrete is *audioasset.Processor (lowered from
// internal/infrastructure/audio so voiceover never imports the
// infrastructure package directly).
type TTSProvider interface {
	Synthesize(ctx context.Context, input TTSInput) (TTSOutput, error)
}

// TTSInput is the canonical wire-shape the use case passes to
// TTSProvider.Synthesize. Mirrors audioasset.AudioInput fields so a
// future thin adapter is a one-line forward.
type TTSInput struct {
	Text string
	// Language is the typed BCP-47 envelope (voiceover.Language).
	// The cross-package seam (useCaseTTSAdapter at
	// internal/app/adapters_voiceover_use_case.go) converts to
	// the raw string when forwarding to audioasset.AudioInput.
	Language      Language
	Voice         string
	Filename      string
	OutputDir     string
	RemoveSilence bool
}

// TTSOutput is the canonical return shape.
type TTSOutput struct {
	LocalPath   string
	CleanedPath string
	Voice       string
	FileHash    string
}

// DestinationResolver is the canonical port for resolving the wire
// DestinationRequest into the canonical ResolvedDestination (folder +
// path + style-group). The production concrete is Service.resolveDestination
// (already implemented in metadata.go).
type DestinationResolver interface {
	Resolve(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error)
}

// VoiceoverDefaultFolderResolver is the canonical port for resolving the
// default-configured Voiceover folder (PR 6 P0.2, June 2026).
//
// Purpose: when a GenerateVoiceoversCommand arrives without
// cmd.Destination, Execute previously short-circuited to
// "missing_folder_id" failure at processOneLanguage (PR-VO-A2 contract
// overload). The fix is a fallback chain at the use case boundary:
//
//	cmd.Destination.FolderID → cfg.Drive.VoiceoverFolder()
//
// The port is a single-method narrow surface so a test stub can return
// ("folder-id", true) or ("", false) without faking the wider service.
// The production concrete is wired in build_bundles_voiceover.go from
// cfg.Drive.VoiceoverFolder() (which delegates to DriveConfig.ResolveFolder).
//
// Resolve semantics:
//   - ("<folderID>", true) : a default folder IS configured; Execute
//     should synthesise a ResolvedDestination
//     with that FolderID and proceed.
//   - ("", false)            : no default folder is configured; Execute
//     surfaces a cross-cutting failure mapping
//     to HTTP 400-equivalent upstream semantics.
type VoiceoverDefaultFolderResolver interface {
	Resolve(ctx context.Context) (driveFolderID, localOutputDir string, ok bool)
}

// AudioPostProcessor is the canonical port for post-TTS audio cleanup
// (silence removal via ffmpeg). Nil-safe at the use case boundary —
// only invoked when cmd.RemoveSilence == true.
type AudioPostProcessor interface {
	Process(ctx context.Context, input AudioPostInput) (AudioPostOutput, error)
}

// AudioPostInput is the canonical input shape.
type AudioPostInput struct {
	LocalPath string
	OutputDir string
	Filename  string
}

// AudioPostOutput carries the cleaned-path surface.
type AudioPostOutput struct {
	CleanedPath string
}

// ────────────────────────────────────────────────────────────────────────
// E1 cutover (June 2026): VoiceoverPublisher replaces AssetLifecycle.
// ────────────────────────────────────────────────────────────────────────
//
// VoiceoverPublisher is the canonical narrow upload-only port. The
// E1 cutover is a structural simplification: the legacy
// AssetLifecycle.Upload (delegating to lifecycle.Service.ProcessAsset)
// bundled Drive upload + dedupe + asset-record persistence. The new
// Publisher is upload-ONLY — it does NOT write to SQLite, does NOT
// run a dedupe gate, does NOT touch the asset-record index.
//
// Publish(ctx, cmd) returns the canonical Drive fileID. Callers
// reconstruct DriveLink + DownloadLink via CanonicalDriveWebURL /
// CanonicalDriveDownloadURL (defined at the bottom of this file) —
// the two helpers are public so usecase.go (processOneLanguage) and
// process_voiceover_item.go (Execute) and any future owner of the
// upload shape can share the canonical URL form without duplicating
// format strings.
//
// In-process pipeline invariant (P0.7 2-PHASE SPLIT, Step 9/12):
// VoiceoverPublisher does NOT hold a tx handle; the per-item
// finalizeStage owns the *sql.Tx and persists the voiceover row
// AFTER Publish returns, so the upload-then-row-write ordering is
// preserved without coupling the publisher to the tx lifetime.
//
// Test-injectable (AGENTS.md Pattern 0): production concrete is
// useCasePublisherAdapter (in internal/app/adapters_voiceover_use_case.go)
// wrapping drive.Admin. Tests inject stubs via UseCaseDeps.Publisher.
type VoiceoverPublisher interface {
	Publish(ctx context.Context, cmd VoiceoverPublishCommand) (fileID string, err error)
}

// VoiceoverPublishCommand is the canonical wire-shape for the upload
// call. Only the 4 fields the upload needs — ID + payload path +
// display filename + destination folder ID. NO metadata/folderPath/
// name/fileHash/source — those concerns live in finalizeStage's per-item
// row OR in the per-style voiceover metadata JSON downstream.
//
// Field semantics:
//   - ID        — caller-derived canonical row ID (buildVoiceoverID
//                 of textHash + lang + folderID); the publisher does
//                 NOT use it for Drive-side identity, but the
//                 downstream finalizeStage uses it for the per-row
//                 insert. This is the value the caller threads
//                 through so a future audit trail can correlate
//                 upload ↔ row.
//   - LocalPath — the post-TTS + post-AudioPostProcessor canonical
//                 audio file on local FS. Publisher does NOT check
//                 file existence; drive.UploadFile returns a clear
//                 error message on a missing payload.
//   - Filename  — the Display Name surfaced on Drive (post-Slugify).
//                 Used as the canonical file label.
//   - FolderID  — the canonical Drive folder ID (post-DestinationResolver).
type VoiceoverPublishCommand struct {
	ID        string
	LocalPath string
	Filename  string
	FolderID  string
}

// TransactionalOutbox is the canonical port for emitting the
// asset.index.requested.v1 envelope inside the caller's tx.
//
// Functionally identical to the existing TxOutboxEnqueuer interface
// (single EnqueueIndexEvent method) — declared here as a
// back-compat alias so the new use case can type-assert against the
// same production concrete *outbox.Dispatcher already wired in
// build_bundles_voiceover.go.
type TransactionalOutbox = TxOutboxEnqueuer

// Logger is intentionally NOT defined as an interface here — the
// canonical codebase-wide logging surface is *zap.Logger (used across
// every application-layer package). The use case constructor accepts
// *zap.Logger directly and nil-safes it via zap.NewNop(). Re-aliasing
// at this layer would only add drift surface; we keep the canonical
// concrete type so the use case is consistent with the rest of the
// codebase.

// ────────────────────────────────────────────────────────────────────────
// BLOC4_ssot_cutover (micro-commit #6, June 2026): FilenameBuilder port.
// ────────────────────────────────────────────────────────────────────────
//
// Extracted from the legacy *Service.buildFilename + the inline
// buildCommandFilename in usecase.go so the new canonical per-item
// use case ProcessVoiceoverItemUseCase (process_voiceover_item.go)
// composes it via a narrow typed port (AGENTS.md Pattern 0).
//
// The production concrete DefaultFilenameBuilder lives in
// filename_builder.go (single implementation for the deployment).
// Tests inject stubs that record invocations.
//
// Signature: BuildFilename(text, language, textHash, template).
//
// Grammar (mirrors the legacy inline logic in filename.go:12 + the
// inline copy in usecase.go's buildCommandFilename):
//
//   {slug} → textutil.SlugifyWithMax(text, 30)
//   {lang} → language (verbatim)
//   {hash} → textHash first 8 chars (or "" when shorter)
//   {time} → time.Now().Format("150405")
//   default template (when empty) → "{slug}_{lang}.mp3"
type FilenameBuilder interface {
	BuildFilename(text, language, textHash, template string) string
}

// ────────────────────────────────────────────────────────────────────────
// VoiceoverFinalizer — unified finalization (P0.4 Fase 3a, July 2026)
// ────────────────────────────────────────────────────────────────────────
//
// VoiceoverFinalizer replaces the two divergent finalization paths
// (child pipeline Stage 4 + legacy batch finalizeStage) with a SINGLE
// 6-step atomic commit sequence inside a caller-owned transaction.
// The caller opens the tx, calls Finalize, then commits.
//
// Steps executed by the concrete finalizer:
//   1. Dedupe gate (CountByDriveFileIDTx + DecideDedupe)
//   2. DELETE old row (DeleteByIDTx)
//   3. INSERT new row (InsertTx)
//   4. media_assets projection (UpsertVoiceoverProjectionTx)
//   5. asset.index.requested outbox (EnqueueIndexEvent)
//   6. voiceover.cleanup.requested outbox (EnqueueCleanupEvent)
//
// Optional steps are nil-safe: dedupe skipped when DriveFileID empty,
// media_assets skipped when LifecycleService nil, outbox skipped when
// nil or FileHash empty, cleanup skipped when ShouldSwap false.
type VoiceoverFinalizer interface {
	Finalize(ctx context.Context, tx *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error)
}

// ────────────────────────────────────────────────────────────────────────
// VoiceoverPostCommitVerifier is the optional narrow port for post-commit
// SQL verification (P0.4 Fase 4a, July 2026). After the tx commits,
// finalizeStage calls Verify(ctx, voiceoverID) to confirm both the
// voiceovers row AND the media_assets projection exist. The verifier
// outcome drives the FinalizeResult.CompletionState typed field
// (audit P0.5, July 2026) so callers can react without parsing log
// lines:
//
//   - nil                                                → CompletionState = StateCompleted
//   - errors.Is(err, ErrReconciliationRequired) == true → CompletionState = StateReconciliationRequired
//   - any other err                                      → CompletionState = StateCompletedUnverified
//
// Nil-safe: when unwired, finalizeStage skips the verification entirely.
// The production concrete queries voiceovers + media_assets via a *sql.DB
// handle passed at composition time.
type VoiceoverPostCommitVerifier interface {
	// Verify confirms that the voiceovers row (id) and the
	// media_assets projection (id, source='voiceover') both exist.
	// Returns nil when both rows are present; returns an error with
	// details about which row is missing — wrap with
	// ErrReconciliationRequired when the voiceovers row itself is
	// missing (severe divergence) so finalizeStage can surface
	// CompletionState=StateReconciliationRequired on FinalizeResult.
	// A bare error (not wrapping ErrReconciliationRequired) signals a
	// warn-level divergence (the projection missing but the canonical
	// row present); finalizeStage maps that to StateCompletedUnverified.
	Verify(ctx context.Context, voiceoverID string) error
}

// ErrReconciliationRequired is the typed severity sentinel a
// VoiceoverPostCommitVerifier.Verify implementer wraps around its
// severe-divergence return values (audit P0.5, July 2026). The
// production adapter wraps the "voiceovers row missing" case with
// this sentinel via fmt.Errorf("...: %w", ErrReconciliationRequired)
// so finalizeStage can react via errors.Is without parsing error
// strings (godlike/07 honest signal; godlike/06 typed-port contract).
//
// Severity contract:
//   - err == nil                                          → StateCompleted
//   - errors.Is(err, ErrReconciliationRequired) == true  → StateReconciliationRequired
//   - any other non-nil err                              → StateCompletedUnverified
//
// Callers MUST NOT compare to this sentinel via == ; the canonical
// match is errors.Is so wrapped errors (with %w) round-trip correctly.
var ErrReconciliationRequired = errors.New("voiceover post-commit verification: reconciliation required (canonical row missing after commit)")

// ────────────────────────────────────────────────────────────────────────
// VoiceoverItemExecutor port (interface forward-declared, BLOC5.4
// implementation pending, June 2026 cutover backend).
// ────────────────────────────────────────────────────────────────────────
//
// VoiceoverItemExecutor is the typed contract for the canonical per-item
// voiceover pipeline. The interface is declared for forward use so
// consumers can adopt a narrow port without depending on the 7-port
// concrete use case directly. The production concrete is wired by the
// composition root when the per-item pipeline lands.
//
// Implements: AGENTS.md Pattern 0 (port abstraction layer).
//
// Note (June 2026 cutover backend): the interface shape is the canonical
// single-method Execute(ctx, *GenerateVoiceoverItemCommand) (*VoiceoverItemResult,
// error). Tests inject stubs that record invocations; production wires
// the canonical concrete in the composition root.
type VoiceoverItemExecutor interface {
	Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error)
}

// ────────────────────────────────────────────────────────────────────────
// E1 cutover (June 2026): canonical Drive URL helpers.
// ────────────────────────────────────────────────────────────────────────
//
// Public so usecase.go (processOneLanguage), process_voiceover_item.go
// (Execute), and any future publisher caller share ONE URL form.
// Duplicating this format string in N call sites would drift over
// time (the legacy inline `drive.google.com/file/d/<id>` literal
// already had two divergent variants in production); centralising
// here kills the drift surface.
//
// Pattern provenance: the canonical webViewLink format is the
// pre-PR-VO-DriveHelper `https://drive.google.com/file/d/<id>/view`
// literal that the lifecycle.Service.ProcessAsset surface returned.
// The download URL pattern is the matching `https://drive.google.com/
// uc?id=<id>&export=download` form that pre-existing
// scripts/handlers used as a download link alt-text. Both match
// the canonical Drive V3 web URL grammar.

// CanonicalDriveWebURL returns the canonical human-facing Drive
// webViewLink for an uploaded Drive file. The result is a verifiable
// URL — clicking in a browser navigates to Drive's preview page for
// the file. Tests pin the form via
// TestVoiceoverPublisher_CanonicalDriveWebURL below.
func CanonicalDriveWebURL(fileID string) string {
	return "https://drive.google.com/file/d/" + fileID + "/view"
}

// CanonicalDriveDownloadURL returns the canonical Drive download
// link for an uploaded Drive file. The result is a verifiable URL
// that, when fetched, returns the file binary. Tests pin the form
// via TestVoiceoverPublisher_CanonicalDriveDownloadURL.
func CanonicalDriveDownloadURL(fileID string) string {
	return "https://drive.google.com/uc?id=" + fileID + "&export=download"
}
