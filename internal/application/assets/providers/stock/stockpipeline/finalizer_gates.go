// Package stockpipeline — finalizer_gates.go (Stock Cutover §12-1, July 2026).

// Stock §12-1 closes the false-success class (P0 2.1 + 2.2 + 2.4 from the
// Stock Production-Ready Plan, document pasted earlier this session).
//
// Three fail-closed gates live here as pure functions:
//
//   - VerifyChunks([]ChunkState) — raises when any chunk lacks
//     LocalPath/SHA256/RemoteFileID, OR when zero chunks were finalized.
//
//   - VerifyMetadata(MetadataState) — raises when the run metadata.json
//     has empty LocalPath/RemoteFileID/SHA256.
//
//   - BuildFinalizationRequest(...) — composes both gates with Lease +
//     ResultManifest + chunk/metadata projections into the canonical
//     finalization.FinalizationRequest, ready for the JobFinalizer.
//
// Rules enforced (per user spec):
//
//	zero chunks finalized          → ErrStockNoChunksFinalized → job FAILED
//	metadata required not published → ErrStockMetadataNotPublished → job FAILED
//	required chunk not finalized    → ErrStockChunkNotFinalized → job non-SUCCEEDED
//	chunk SHA256 empty              → ErrStockChunkHashMissing (P0 2.4 — pre-publish)
//
// Idempotency: BuildFinalizationRequest is pure — it builds the SAME
// request from the SAME inputs, so a replay (retry on transient broker
// failure) produces a byte-stable FinalizationRequest that the
// JobFinalizer.IdempotencyCache + UNIQUE(job_id, attempt, result_hash)
// index collapses to a single row.
//
// Split topology (godlike/06 SSOT one-canonical-owner-per-fact):
//   - finalizer_gates.go        — types + sentinels + const (this file)
//   - finalizer_gates_verify.go — VerifyChunks + VerifyMetadata
//   - finalizer_gates_build.go  — BuildFinalizationRequest + OrchestrationResult
package stockpipeline

import (
	"errors"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Sentinel errors (godlike/07 typed-error contract) ───────────────
//
// Callers MUST use errors.Is(err, ErrStock*) to inspect the failure
// class. Each sentinel names the rule it enforces so a log scan can
// grep by rule-id without parsing human-readable suffix.

var (
	// ErrStockNoChunksFinalized is raised when the orchestrator's
	// verify_chunks gate observes zero chunks at finalize time.
	// Per P0 2.1 ("zero artifact finalizzati → job FAILED").
	// Until Commit 4-7 wires the Cut → Render → Stage → Publish
	// chunk-rendering ladder, this gate raises on EVERY run —
	// closing the silent-success class: a 5-artifact stub
	// manifest with Required:false no longer passes the gate.
	ErrStockNoChunksFinalized = errors.New("stock: zero chunks finalized — job cannot SUCCEEDED (P0 2.1)")

	// ErrStockChunkNotFinalized is raised when a chunk's
	// RemoteFileID is empty after Publish, OR when its LocalPath
	// is empty (render failed). Per P0 2.1 ("un chunk required
	// non finalizzato → job non SUCCEEDED"). The chunk itself
	// may be partially on Drive (Publisher returned a corrupt
	// DriveLink); the operator can backfill from ChunkState.RemoteFileID
	// but the JOB ledger MUST stay non-SUCCEEDED until every
	// required chunk's RemoteFileID is non-empty.
	ErrStockChunkNotFinalized = errors.New("stock: chunk not finalized after publish (P0 2.1)")

	// ErrStockChunkHashMissing is raised when a chunk has empty
	// SHA256 at verify_chunks time. Per P0 2.4 ("render → stat →
	// SHA256 → VerifiedArtifact"): SHA256 is computed BEFORE
	// publishing and feeds the idempotency-key, source-version,
	// asset-version, outbox envelope, dedup, diagnostic, and
	// the Qdrant index event's source_version. Sending an empty
	// SHA256 to the indexer is terminal-rejected by the consumer,
	// so we fail-closed at the orchestrator rather than dispatching
	// an event the consumer rejects.
	ErrStockChunkHashMissing = errors.New("stock: chunk SHA256 empty — must compute pre-publish (P0 2.4)")

	// ErrStockChunkHashInvalid is raised when a chunk's SHA256 is
	// non-empty but does NOT match the canonical hex-encoded
	// SHA-256 format (exactly 64 lowercase hex chars). Per
	// P0 2.4 hardening (Commit 0.2 — godlike/07 fail-closed
	// at the gate layer): the orchestrator refuses to dispatch
	// a chunk whose SHA256 is malformed because the
	// IdempotencyKey derivation (prefix + sha[:16]) would have
	// PANICKED at runtime on short input. The error wraps the
	// canonical domainasset.ErrSHA256Invalid so callers can
	// errors.Is both sentinels to inspect the failure class.
	ErrStockChunkHashInvalid = errors.New("stock: chunk SHA256 malformed — must be exactly 64 lowercase hex chars (P0 2.4)")

	// ErrStockMetadataHashInvalid mirrors ErrStockChunkHashInvalid
	// for the per-run metadata.json (symmetric gate).
	ErrStockMetadataHashInvalid = errors.New("stock: metadata SHA256 malformed — must be exactly 64 lowercase hex chars (P0 2.4)")

	// ErrStockChunkLocalMissing is raised when a chunk's LocalPath
	// does not exist on disk at finalize time. Per P0 2.1 the
	// chunk cannot be re-published from a missing file; the
	// orchestrator MUST preserve render output until
	// CompleteWithArtifacts commits so rollback doesn't poison
	// a retry path.
	ErrStockChunkLocalMissing = errors.New("stock: chunk local file not on disk at finalize time")

	// ErrStockMetadataNotPublished is raised when the per-run
	// metadata.json has empty LocalPath, RemoteFileID, or SHA256
	// at verify_metadata time. Per P0 2.1 ("metadata required
	// non pubblicato → job FAILED"). Metadata carries the
	// per-run chunk envelope that lets a downstream consumer
	// reconstruct the run; without it the run is unintelligible.
	ErrStockMetadataNotPublished = errors.New("stock: metadata.json not published (P0 2.1)")
)

// StockTimestampPolicyVersionV1 is the canonical fallback policy
// version stamped on ChunkState.PolicyVersion when RunInput.PolicyVersion
// is empty. PR-004 (July 2026) introduces this typed constant for
// the godlike/06 SSOT literal — single canonical source for the
// fallback. Used in StockPublishStep.Run when
// strings.TrimSpace(in.PolicyVersion) == "". Locked at "v1" until
// a future policy break justifies a v2 — the wire-shape field
// already exists today (filled in buildStockRunMetadata +
// buildRunSummary from the same RunInput.PolicyVersion) so the
// fallback is a defensible default, not a wire-shape change.
const StockTimestampPolicyVersionV1 = "stock_timestamp_v1"

// ChunkState captures the state of a single chunk at finalize
// time. Built by the future Commit 4-7 chunk-rendering ladder
// (render → ComputeAndFillSHA256 → publisher.Publish → fill
// RemoteFileID + RemoteWebViewLink). The orchestrator's
// verify_chunks step rejects any chunk that lacks RemoteFileID,
// SHA256, or a non-zero LocalPath — see VerifyChunks for the
// composition.
type ChunkState struct {
	Index int // 0-based chunk index (stamped on ArtifactID + SourceVersion for sort stability)

	ArtifactID string // stable ID; canonical convention "stock:<run_fingerprint>:chunk:<index>"

	Filename string // leaf name for publication (e.g. "stock_<run>_<index>.mp4")

	LocalPath string // render output on disk; checked present + non-zero size

	SourceURL string // original source URL for this timestamp / clip

	SourceProvider string // canonical provider bucket (youtube/pexels/pixabay/unknown) — inferred at plan-build time, copied verbatim from ClipPlan.SourceProvider per godlike/06 SSOT

	SourceVideoID string // canonical provider-native ID (YouTube video ID when SourceProvider == youtube; empty otherwise)

	TotalChunks int // per-run total chunk count = len(runner.State().Plan) at chunk-build time; repeated per-entry per user spec (godlike/07 minimum-blast-radius acknowledges logical duplication)

	DrivePath string // PR-004 (July 2026): canonical per-chunk Drive webview link captured from PublishedArtifact.Location.WebViewLink at chunk-build time. Duplicate of RemoteWebViewLink by design — godlike/06 SSOT keeps a single source (Location.WebViewLink); the second field name is for the Qdrant semantic-payload enrichment wave which expects the wire-shape key drive_path (vs the legacy remote_web_view_link). For godlike/07 minimum-blast-radius the duplication is acknowledged in this godoc rather than reflected in metadata.json (per godlike/07 no-fake-availability the wire-shape delta is forward-pointer to the Qdrant pipeline not the stock metadata.json).

	PolicyVersion string // PR-004 (July 2026): per-run policy version tag. Source: RunInput.PolicyVersion (operator-supplied) with hardcoded fallback to StockTimestampPolicyVersionV1 ("stock_timestamp_v1") when empty. Pre-computed ONCE per run by StockPublishStep.Run and stamped on every chunk for trace-back traceability.

	TimestampDriveFolderLink string // WebViewLink of the parent timestamp Drive folder. Captured from PublishedArtifact.Location.FolderID via the canonical https://drive.google.com/drive/folders/{FOLDER_ID} construction. Propagated to Qdrant payload as "timestamp_drive_folder_link" for "open in Drive" navigation from search results. Per-run scalar (all chunks in the same timestamp block share the same parent folder).

	TimestampFolderID string // Google Drive folder ID of the parent timestamp folder. Captured from PublishedArtifact.Location.FolderID. Propagated to Qdrant payload as "timestamp_folder_id" for programmatic Drive API access.

	StartSec float64 // clip start timestamp in seconds

	EndSec float64 // clip end timestamp in seconds

	Title string // human label propagated into metadata.json

	Description string // human-readable English summary propagated into metadata.json

	// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): the 4 typed
	// content fields travel ClipSpec → ClipPlan → ChunkState →
	// ChunkMetadataEntry verbatim. Each has omitempty in the JSON
	// wire so deterministic-planner runs (where the 4 stay at
	// zero/nil) produce the same wire shape as the pre-PR baseline.

	// Round is the boxing-style round number. Zero is the canonical
	// "not specified" value; deterministic planner runs leave it at
	// zero.
	Round int

	// Tags is the per-clip free-form tag list. nil-or-empty both
	// serialize as absent (omitempty on the metadata entry).
	Tags []string

	// Category is the content category (boxing / running / etc.).
	Category string

	// Slug is the explicit operator-supplied Drive folder slug.
	// When non-empty, it wins over the title-derived slug in
	// perClipLeafName (the publisher's leaf derivation). Empty
	// means "derive slug from title" (pre-PR behavior).
	Slug string

	SHA256 string // hex-encoded SHA-256 of LocalPath (populated by ComputeAndFillSHA256)

	SizeBytes int64 // os.Stat(LocalPath).Size() at hash time

	RemoteFileID string // publisher.Publish result

	RemoteWebViewLink string // publisher.Publish result

	RemoteDownloadLink string // publisher.Publish result (may be empty for some providers)
}

// ComputeAndFillSHA256 reads the file at cs.LocalPath and populates
// cs.SHA256 + cs.SizeBytes. Standalone helper so the future
// Commit 4-7 chunk-rendering ladder can call stat → SHA256 →
// publish in the canonical order (P0 2.4). On stat/hash error
// the field is left empty so VerifyChunks surfaces
// ErrStockChunkLocalMissing / ErrStockChunkHashMissing.
//
// PR-REFACTOR-P0-IO-BINDER (July 2026): uses LocalFSPort instead
// of os.Stat. nil fs is fail-closed — returns ErrStockChunkLocalMissing.
//
// Caveat: read-only call on cs. Mutates cs only — safe under
// sequence-point guard because callers invoke this BEFORE publishing.
// Returns sentinel errors verbatim so VerifyChunks can errors.Is()
// without re-deriving the failure class.
func (cs *ChunkState) ComputeAndFillSHA256(fs LocalFSPort) error {
	if cs.LocalPath == "" {
		return ErrStockChunkLocalMissing
	}
	if fs == nil {
		return fmt.Errorf("%w: %s (LocalFSPort not wired)", ErrStockChunkLocalMissing, cs.LocalPath)
	}
	fi, statErr := fs.Stat(cs.LocalPath)
	if statErr != nil {
		return fmt.Errorf("stock: stat %s: %w", cs.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%w: %s (size=0)", ErrStockChunkLocalMissing, cs.LocalPath)
	}
	h, hashErr := job.ComputeSHA256(cs.LocalPath)
	if hashErr != nil {
		return fmt.Errorf("stock: sha256(%s): %w", cs.LocalPath, hashErr)
	}
	cs.SHA256 = h
	cs.SizeBytes = fi.Size()
	return nil
}

// MetadataState captures the state of the per-run metadata.json
// artifact at finalize time. Treat it like a single ChunkState
// but with metadata-specific MIMEType (application/json) and a
// stable ArtifactID convention independent of chunk indices.
type MetadataState struct {
	LocalPath         string
	SHA256            string
	SizeBytes         int64
	RemoteFileID      string
	RemoteWebViewLink string
}
