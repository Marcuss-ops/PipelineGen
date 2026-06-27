// Package clips (clip_ops) — port-typed orchestration for the
// Reconcile / Cleanup / VerifyClip jobs previously living in
// internal/api/assets/clips/clip_ops.go.
//
// Wave 14 PR2 (June 2026): migration target. The previous file
// reached into *assets.ClipsRepository (concrete sqlite repo),
// *assets.VoiceoversRepository (concrete), *assets.ImagesRepository
// (concrete), *deletion.DeletionService (application-side, OK) and
// jobservice.Service (domain interface, OK). Two of those — the
// three repos — were on the docs/migrations/api-infrastructure-
// imports-allowlist.txt grandfathered list. This file ports the
// orchestration onto the typed ClipRepositoryPort /
// VoiceoverRepositoryPort / ImageRepositoryPort /
// ClipDriveUploaderPort ports, so the API handler can call into
// NewClipOpsService(deps) without itself importing infrastructure.
//
// The Service returned here is invoked from api/clips/handler.go's
// HTTP methods Reconcile, Cleanup, VerifyClip — those become thin
// transport shims over the Service methods below.
package clips

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Typed error sentinels (S1d + Wave 22 PR-5 polish, June 2026) ─────
//
// Pair with errors.Is for branching on caller-side. The handler layer
// (api/assets/clips/clip_ops.go::HandleFixHash) translates each sentinel
// into the canonical HTTP status code:
//
//	ErrFixHashVoiceoverUnsupported   → 400 (unsupported source)
//	ErrFixHashMissingDriveLink       → 409 (clip has no Drive mirror)
//	ErrFixHashDispatcherUnavailable  → 503 (dispatcher not wired)
//	ErrJobsUnavailable               → 503 (jobs service not wired — Wave 22 PR-5)
//
// Other errors (decode / repo read / Drive API) fall through as
// 500 Internal Error with a typed wrap.
var (
	ErrFixHashVoiceoverUnsupported  = errors.New("fix-hash not supported for voiceover source")
	ErrFixHashMissingDriveLink      = errors.New("fix-hash: clip has no drive_link / download_link to query")
	ErrFixHashDispatcherUnavailable = errors.New("fix-hash: asset-mutation dispatcher not wired")

	// ErrJobsUnavailable is the typed sentinel returned by Cleanup
	// when s.jobs (JobsServicePort) is nil — test fixtures, partial
	// deployments, or composition roots that assemble the service
	// without wiring jobs. Callers (the api handler) surface this
	// as 503 Service Unavailable. Matches the S1d
	// ErrFixHashDispatcherUnavailable pattern: errors.Is-friendly
	// sentinel + nil-port guard, so dashboards can distinguish
	// "service unconfigured" (this) from "transient dispatcher
	// reject" (the dispatcher's own runtime errors).
	ErrJobsUnavailable = errors.New("cleanup requires jobs service (no sync pagination fallback — use POST /:source/cleanup)")
)

// (CleanupServicePort + JobsServicePort live below as interfaces that the
// composition root will adapt to *deletion.DeletionService and
// domain/job.Service. We deliberately do NOT import either internal-package
// type here — api/clips/handler.go and the composition root remain the
// only places that bridge the canonical concrete services into these
// narrow ports.)

// ── Sentinel errors (PR 4, June 2026 — codex/clips-reconcile-real) ────

// ErrQueueUnavailable surfaces when JobsServicePort is nil at
// composition time. The HTTP handler maps this to 503 + code
// RECONCILE_QUEUE_UNAVAILABLE (agent-facing contract). Use
// errors.Is(err, ErrQueueUnavailable) instead of string-matching.
var ErrQueueUnavailable = errors.New("clips: Reconcile queue unavailable (JobsServicePort not wired)")

// ErrInvalidSource surfaces when an unknown source is passed. The
// HTTP handler maps this to 400 Bad Request. Use errors.Is instead
// of string-matching. PR 4 replaces the pre-existing
// `fmt.Errorf("invalid source: %s", ...)` returns with a typed
// sentinel wrapped via fmt.Errorf("%w: %s", ErrInvalidSource, source).
var ErrInvalidSource = errors.New("clips: invalid source")

// ── Voiceover / Images / Jobs / Deletion port surface for cleanup ────

// CleanupServicePort is the narrowed surface of *deletion.DeletionService
// consumed by clip_ops. The full DeletionService has many other
// methods; we expose only what Reconcile/Cleanup/VerifyClip need.
type CleanupServicePort interface {
	CleanupOrphanFiles(ctx context.Context, path string, dryRun bool) (int, error)
	DeleteClip(ctx context.Context, source, clipID string, hardDelete bool) error
}

// JobsServicePort is the narrowed surface of `jobservice.Service`
// for enqueuing "system.cleanup" jobs in deep mode. Repurposes the
// existing port `domain/job` to avoid a re-import in this file.
type JobsServicePort interface {
	Enqueue(ctx context.Context, req JobsEnqueueRequest) (*JobsEnqueueResponse, error)
}

// JobsEnqueueRequest is a small DTO that mirrors the relevant fields
// of the canonical `*job.EnqueueRequest` so this file avoids importing
// domain/job (kept minimal — the adapter at the composition root
// builds the canonical request).
type JobsEnqueueRequest struct {
	Type      string
	Payload   map[string]any
	Priority  int
	ActiveKey string
}

// JobsEnqueueResponse mirrors the relevant fields of the canonical
// `*job.Job` so handlers can render {job_id: ...} without importing
// the canonical domain type. Adapter: minimal projection at the
// composition root.
type JobsEnqueueResponse struct {
	ID string
}

// ── ClipOps service ──────────────────────────────────────────────────

// ClipOpsService owns the orchestration behind the HTTP verbs
// Reconcile / Cleanup / VerifyClip. Construction via NewClipOpsService;
// every required port is passed in. Method semantics match the
// pre-PR2 api-side copy 1:1.
//
// S1d (June 2026): the `dispatcher` field is added so the service can
// route fix-hash recovery (POST /:source/clips/:id/fix-hash) through
// the canonical AssetMutationDispatcher port — exactly the PR-CLIP-
// RAW-MUTATIONS gate that bans raw repo writes in production. The
// handler currently inlines the same flow (see internal/api/assets/
// clips/clip_ops.go::HandleFixHash) per minimal-scope Wave 14
// migration policy. The migration target is unwired at composition
// time today; once Reconcile/Cleanup/Verify move into this service
// (current Wave 14 migration), the dispatcher arg becomes
// production-required.
type ClipOpsService struct {
	sourceResolver SourceResolverPort
	voiceoverRepo  VoiceoverRepositoryPort
	imagesRepo     ImageRepositoryPort
	driveUploader  ClipDriveUploaderPort
	cleanup        CleanupServicePort
	jobs           JobsServicePort
	dispatcher     ClipIndexDispatcherPort
	log            *zap.Logger
}

// NewClipOpsService constructs the canonical service. Pass nil for
// ports that callers don't use (test fixtures, partial deployments);
// the corresponding service methods will internal-error / no-op per
// the legacy semantics.
//
// S1d (June 2026): the dispatcher argument is appended last so the
// migration target's constructor signature is forward-compatible:
// callers who construct the service today for tests can pass nil
// (the service's FixHash method returns ErrDispatcherUnavailable
// in that case). Once the handlers route through this service
// (Wave 14 follow-up), the dispatcher will be required.
func NewClipOpsService(
	sourceResolver SourceResolverPort,
	voiceoverRepo VoiceoverRepositoryPort,
	imagesRepo ImageRepositoryPort,
	driveUploader ClipDriveUploaderPort,
	cleanup CleanupServicePort,
	jobs JobsServicePort,
	dispatcher ClipIndexDispatcherPort,
	log *zap.Logger,
) *ClipOpsService {
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipOpsService{
		sourceResolver: sourceResolver,
		voiceoverRepo:  voiceoverRepo,
		imagesRepo:     imagesRepo,
		driveUploader:  driveUploader,
		cleanup:        cleanup,
		jobs:           jobs,
		dispatcher:     dispatcher,
		log:            log,
	}
}

// ReconcileCommand is the typed command for ClipOpsService.Reconcile.
// Mirrors the JSON-shape the HTTP handler accepts (source,
// folder_id, fix, dry_run). PR 4 (June 2026 — codex/clips-reconcile-real).
type ReconcileCommand struct {
	Source   string `json:"source"`
	FolderID string `json:"folder_id,omitempty"`
	Fix      bool   `json:"fix"`
	DryRun   bool   `json:"dry_run"`
}

// ReconcileStarted is the application-side response shape. The HTTP
// handler composes the StatusURL (literal "/api/jobs/{id}") so the
// application service stays URL-agnostic.
type ReconcileStarted struct {
	JobID     string
	ActiveKey string
}

// Reconcile enqueues a durable catalog.sync job. PR 4
// (codex/clips-reconcile-real, June 2026) replaces the previous
// stub-by-log body with a real Job-enqueue + handler-computed
// StatusURL flow.
//
// Returns:
//   - ErrQueueUnavailable when s.jobs is nil (composition bug or
//     partial deploy). Handler maps to HTTP 503 + code
//     RECONCILE_QUEUE_UNAVAILABLE.
//   - A wrapped enqueue error when the broker rejects the job.
//     Handler maps to HTTP 500.
//   - On success: *ReconcileStarted{JobID, ActiveKey}.
func (s *ClipOpsService) Reconcile(ctx context.Context, cmd ReconcileCommand) (*ReconcileStarted, error) {
	if s.jobs == nil {
		return nil, ErrQueueUnavailable
	}
	activeKey := reconcileActiveKey(cmd)
	payload := map[string]any{
		"source":    cmd.Source,
		"folder_id": cmd.FolderID,
		"fix":       cmd.Fix,
		"dry_run":   cmd.DryRun,
	}
	enqueued, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type:      job.TypeCatalogSync,
		Payload:   payload,
		Priority:  10,
		ActiveKey: activeKey,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue reconcile job: %w", err)
	}
	if enqueued == nil || enqueued.ID == "" {
		return nil, fmt.Errorf("enqueue reconcile job: empty job id returned by broker")
	}
	if s.log != nil {
		s.log.Info("reconcile durable job enqueued",
			zap.String("job_id", enqueued.ID),
			zap.String("active_key", activeKey),
			zap.String("source", cmd.Source),
			zap.String("folder_id", cmd.FolderID),
			zap.Bool("fix", cmd.Fix),
			zap.Bool("dry_run", cmd.DryRun))
	}
	return &ReconcileStarted{
		JobID:     enqueued.ID,
		ActiveKey: activeKey,
	}, nil
}

// reconcileActiveKey derives a deterministic, idempotent ActiveKey
// from the ReconcileCommand. The format guarantees cross-process
// deduplication: the same {source, folder_id, fix, dry_run} tuple
// maps to the same key, regardless of which worker picks it up.
func reconcileActiveKey(cmd ReconcileCommand) string {
	return fmt.Sprintf("reconcile_%s_%s_%v_%v",
		cmd.Source, cmd.FolderID, cmd.Fix, cmd.DryRun)
}

// CleanupInput captures the request shape for Cleanup. The HTTP
// method on the api side ShouldBindJSON's this directly.
//
// PR 5 (June 2026 — codex/clips-cleanup-job): CheckLocal + Repair +
// explicit Delete flag were added to the input. The synchronous
// report-shape is gone; the service now enqueues a durable
// assets.cleanup job (batch=250 + cursor) and returns *CleanupStarted.
type CleanupInput struct {
	Source     string
	DryRun     bool
	CheckLocal bool
	CheckDrive bool
	Repair     bool
	Delete     bool
	// Deep is preserved for backward compatibility with the previous
	// `deep=true + source=all` branch semantics; the assets.cleanup
	// handler does not currently branch on it (deep is implied by
	// the per-clip Repair + Delete flags).
	Deep bool
	// BatchSize overrides the canonical 250 row-per-batch cap when
	// non-zero. The cleaner logs a warning when this exceeds 250.
	BatchSize int
}

// CleanupReport is the JSON shape returned to the caller for the
// pre-PR5 synchronous path. PR 5 retired this shape entirely; the
// HTTP handler now returns *CleanupStarted (job_id + status_url)
// and clients poll `/api/jobs/{id}/full` for the final report.
//
// Kept as a struct (with all fields unused) so any pre-PR5 callers
// that imported the type continue to compile; new code MUST use
// *CleanupStarted instead.
type CleanupReport struct {
	OK         bool
	Source     string
	JobID      string // populated when deep=true & jobs service is wired
	DryRun     bool
	CheckDrive bool
	Checked    int
	Deleted    int
	Summary    string
	Message    string
	Items      []CleanupItem
}

// CleanupItem is a per-clip row in the (deprecated) report shape.
type CleanupItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Cleanup enqueues a durable assets.cleanup job and returns
// *CleanupStarted. The synchronous 10K-ListClipsPaged + per-clip
// verify-and-delete loop is REMOVED; the durable handler in
// internal/application/assets/cleanup owns scan / classify /
// repair / delete. Dirty operators' clients still work — the
// HTTP handler now returns `{ok, status:"queued", job_id,
// status_url: "/api/jobs/<id>", source, dry_run, check_local,
// check_drive, repair, delete}` and clients poll the job URL.
//
// Returns:
//   - ErrJobsUnavailable when s.jobs is nil (composition bug — Wave 22 PR-5 polish uses
//     the typed ErrJobsUnavailable from the fix-hash sentinel block above; aliases
//     the pre-PR5 ErrQueueUnavailable pattern).
//   - ErrInvalidSource when source is not in the allowlist.
//   - Enqueue error wrapped via fmt.Errorf("enqueue cleanup job: %w").
func (s *ClipOpsService) Cleanup(ctx context.Context, in CleanupInput) (*CleanupStarted, error) {
	if s.jobs == nil {
		return nil, ErrJobsUnavailable
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		return nil, fmt.Errorf("%w: empty source", ErrInvalidSource)
	}
	// PR 5 (June 2026 - codex/clips-cleanup-job): replaced the
	// pre-PR5 synchronous repo-resolve preflight (which violated the
	// "drop the synchronous scan" spec clause) with a static
	// allowlist of canonical sources. The async assets.cleanup
	// handler owns source resolution + iteration; this gate only
	// ensures the caller named a recognised source before enqueue
	// (returns 400 + ErrInvalidSource otherwise).
	knownSources := map[string]bool{
		"youtube":   true,
		"artlist":   true,
		"stock":     true,
		"voiceover": true,
		"images":    true,
		"all":       true,
	}
	if !knownSources[source] {
		return nil, fmt.Errorf("%w: %s", ErrInvalidSource, in.Source)
	}
	activeKey := cleanupActiveKey(in)
	payload := cleanupJobPayload(in)
	jobResp, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type:      job.TypeAssetsCleanup,
		Payload:   payload,
		Priority:  10,
		ActiveKey: activeKey,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue cleanup job: %w", err)
	}
	if jobResp == nil || jobResp.ID == "" {
		return nil, fmt.Errorf("enqueue cleanup job: empty job id returned by broker")
	}
	if s.log != nil {
		s.log.Info("cleanup durable job enqueued",
			zap.String("job_id", jobResp.ID),
			zap.String("active_key", activeKey),
			zap.String("source", in.Source),
			zap.Bool("dry_run", in.DryRun),
			zap.Bool("check_local", in.CheckLocal),
			zap.Bool("check_drive", in.CheckDrive),
			zap.Bool("repair", in.Repair),
			zap.Bool("delete", in.Delete))
	}
	return &CleanupStarted{
		JobID:     jobResp.ID,
		ActiveKey: activeKey,
		BatchSize: in.BatchSize,
	}, nil
}

// cleanupActiveKey derives a deterministic, idempotent ActiveKey
// from the CleanupInput. Cross-process deduplication: the same
// {source, dry_run, check_local, check_drive, repair, delete, batch}
// tuple maps to the same key.
func cleanupActiveKey(in CleanupInput) string {
	return fmt.Sprintf("cleanup_%s_%v_%v_%v_%v_%v_%d",
		in.Source, in.DryRun, in.CheckLocal, in.CheckDrive, in.Repair, in.Delete, in.BatchSize)
}

// cleanupJobPayload builds the JSON-shaped payload passed to the
// assets.cleanup handler. Mirrors the spec list verbatim.
//
// PR 5 additions: CheckLocal + Repair + BatchSize (configurable
// cap; cleaner logs warn when it exceeds 250).
func cleanupJobPayload(in CleanupInput) map[string]any {
	p := map[string]any{
		"source":      in.Source,
		"dry_run":     in.DryRun,
		"check_local": in.CheckLocal,
		"check_drive": in.CheckDrive,
		"repair":      in.Repair,
		"delete":      in.Delete,
		"batch_size":  in.BatchSize,
	}
	return p
}

// VerifyInput captures the request shape for VerifyClip.
type VerifyInput struct {
	Source string
	ClipID string
}

// VerifyReport mirrors the pre-PR2 api-output keys for VerifyClip.
// Field set is the same; the type is typed so the API layer never
// imports domain/asset to construct the report.
//
// S1c (June 2026) — VerifyClip is now strictly read-only. When the
// local hash is missing but Drive supplies an MD5, the report
// exposes the recoverable value through `HashRecoverable` +
// `HashRecoverableValue` rather than silently writing back to the
// DB. The legacy `HashRecovered` field signals "recovered but NOT
// written" so dashboards can show a clear distinction: this clip
// CAN be fixed (HashRecoverable=true) but Verify itself does NOT
// fix it. Recovery is a separate, explicit operator action
// (`POST /:source/clips/:id/fix-hash`, Wave 22 task 5 / PR-CLIP-
// RAW-MUTATIONS follow-up).
//
// S1d (June 2026) — VerifyReport gains the typed `HashInfo` field
// as the canonical informational channel for the "Drive has a
// candidate MD5 we COULD persist" signal. The legacy flat fields
// (HashRecovered, HashRecoverable, HashRecoverableValue) are
// preserved for back-compat JSON consumers but are no longer
// accompanied by the `hash_recoverable_from_drive` slug in
// `Issues[]`. Read `HashInfo` for the canonical typed shape.
type VerifyReport struct {
	OK     bool
	Source string
	ClipID string
	// Issues carries BLOCKING issues only — i.e. conditions that
	// genuinely require operator attention AND flip Coherent=false.
	// Informational signals (the canonical example: "Drive has a
	// recoverable MD5 we could persist") live in their own typed
	// fields (HashInfo). The pre-S1c pattern that mixed both lists
	// in Issues[] was retired for S1d (the slug
	// "hash_recoverable_from_drive" was REMOVED from this slice
	// and migrated to HashInfo.Recoverable / HashInfo.CandidateHash).
	//
	// Wave 22 PR-5 polish (June 2026): godoc enforcement of this
	// contract is the lightest-touch split. A typed Issue {Slug,
	// Category} struct would be the more rigorous choice but
	// changes the JSON wire shape for CURRENT callers (Issues[0]
	// becomes an object instead of a string) — deferred to a
	// follow-up PR with a v1→v2 envelope migration.
	//
	// Current blocking-slot slugs (June 2026):
	//   local_file_missing, local_path_empty,
	//   drive_link_missing, drive_link_invalid,
	//   hash_missing, invalid_source
	// Add new slugs ONLY after reviewing the S1d channel-separation
	// rule above.
	Issues         []string
	DB             bool
	LocalFile      bool
	LocalPath      string
	LocalError     string
	HasDriveLink   bool
	DriveLink      string
	DriveFileID    string
	DriveLinkValid bool
	Hash           string
	HasHash        bool
	HashVerified   bool
	// ── Canonical S1d informational channel (NEW — read this for new code) ──  (S1d code-reviewer Finding 2)
	//
	// HashInfo is the S1d (June 2026) typed informational channel
	// for the "Drive has a candidate MD5 we could persist" signal.
	// Populated ONLY by the verifyClip read-only path when:
	//   (1) the local clip row has no FileHash, AND
	//   (2) the extracted Drive fileID is non-empty, AND
	//   (3) driveUploader.GetFileMD5 returned a non-empty value.
	// The verify read-only path MUST NOT append the slug
	// "hash_recoverable_from_drive" to Issues[] even though the
	// recoverable signal IS present. See the HashInfo struct
	// godoc immediately below the VerifyReport block for the
	// full CRITICAL CONTRACT (Recoverable/CandidateHash/Recovered
	// sub-fields + the canonical "informational channel separation"
	// rationale).
	HashInfo HashInfo
	// ── Legacy flat fields — JSON back-compat ONLY. Read HashInfo above for canonical semantics. ──
	// HashRecovered is the legacy `did verify itself write a hash?`
	// flag. Always FALSE on the S1c-era read-only verify path —
	// pre-S1c code set it true after a silent repo.Upsert, but
	// S1c retired the silent write and S1d confirmed this field
	// stays false forever on verify. JSON consumers pivot on the
	// pre-S1c semantics risk; new consumers should read HashInfo
	// for the canonical inferred-Recovered signal (which today is
	// also false — see HashInfo SCOPE BOUNDARY note). The flat
	// field is KEPT here for back-compat JSON consumers reading
	// report.HashRecovered; never write to it from the verify
	// path (will always be false). Post-S1d never true.
	//
	// Wave 22 PR-5 polish (the legacy godoc was too terse to
	// communicate the always-false contract — replacing now).
	HashRecovered        bool
	HashRecoverable      bool   // legacy: drive supplied a candidate that COULD be persisted (kept for back-compat)
	HashRecoverableValue string // legacy: the value Drive returned; populated when HashRecoverable=true
	FolderID             string
	FolderPath           string
	Status               string
	Coherent             bool
	IssueCount           int
	Extra                map[string]any // catch-all for adapter-extended fields
}

// HashInfo is the S1d (June 2026) typed informational channel for
// the "Drive supplies a candidate MD5 we could persist" case.
// The verifyClip read-only path fills this struct when Drive
// supplies a candidate that the clip row's FileHash column is
// currently missing.
//
// CRITICAL CONTRACT (S1d): the verify read-only path MUST NOT
// append the slug "hash_recoverable_from_drive" to
// VerifyReport.Issues[] even though the recoverable signal IS
// present. The candidate refilling the canonical issue list would
// (a) bump IssueCount and (b) flip Coherent to false — both
// incorrect for a state whose canonical remedy is the explicit
// operator action POST /:source/clips/:id/fix-hash. HashInfo
// carries the same signal without polluting the coherence verdict.
//
// SCOPE BOUNDARY (code-review Finding 1, June 2026): a previous
// draft of HashInfo carried a `Recovered` boolean to mark whether
// a writer path (FixHash) had persisted the candidate. That field
// was REMOVED because no producer-path populates it (verify
// unconditionally sets false; FixHash returns its own
// FixHashReport and never reads from / writes to HashInfo). A
// future PR may add a `VerificationPostFix` hook that reads the
// persisted state and updates HashInfo, but the field is omitted
// today to avoid the silent drift hazard (a future maintainer
// writing a test that asserts `HashInfo.Recovered == true` after
// fix-hash would always fail). Recovery status is authoritative
// on FixHashReport.OK / FixHashReport.Reindexed (independent
// surface, intentionally separate from HashInfo).
type HashInfo struct {
	// Recoverable is true when Drive supplied a non-empty MD5 for
	// the local clip row that had no FileHash. Same semantic as
	// the legacy HashRecoverable bool (kept for back-compat).
	Recoverable bool
	// CandidateHash is the value Drive returned. Populated only
	// when Recoverable=true. Mirrors legacy HashRecoverableValue
	// (kept for back-compat). Pure-string, no transformation: it
	// is the EXACT value driveUploader.GetFileMD5 returned (caller
	// is responsible for any case-normalisation before persisting).
	CandidateHash string
}

// Verify reports DB/local/Drive coherence for a single clip.
func (s *ClipOpsService) Verify(ctx context.Context, source, clipID string) *VerifyReport {
	report := &VerifyReport{
		OK:     true,
		Source: source,
		ClipID: clipID,
		Issues: []string{},
		DB:     true,
		Extra:  map[string]any{},
	}

	if clipID == "" {
		report.OK = false
		return report
	}

	// Handle Voiceover source.
	if strings.ToLower(source) == "voiceover" && s.voiceoverRepo != nil {
		rec, err := s.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			report.OK = false
			return report
		}
		if rec == nil {
			report.OK = false
			return report
		}
		// Synthesize domain clip from the DTO and run verify
		clip := voiceoverDTOToClip(rec)
		return s.verifyClip(ctx, source, nil, clip)
	}

	repo := s.resolveRepo(source)
	if repo == nil {
		report.OK = false
		report.Issues = append(report.Issues, "invalid_source")
		return report
	}

	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		report.OK = false
		return report
	}
	return s.verifyClip(ctx, source, repo, clip)
}

// verifyClip is the private verifier. Mirrors the legacy verifyClip
// in api/clip_ops.go; takes a repo (might be nil for voiceover
// source) and a clip.
func (s *ClipOpsService) verifyClip(ctx context.Context, source string, repo ClipRepositoryPort, clip *asset.Asset) *VerifyReport {
	report := &VerifyReport{
		OK:     true,
		Source: source,
		ClipID: clip.ID,
		Issues: []string{},
		DB:     true,
		Extra:  map[string]any{},
	}

	// Check local file
	hasLocalFile := false
	if clip.LocalPath() != "" {
		if _, statErr := os.Stat(clip.LocalPath()); statErr == nil {
			hasLocalFile = true
			report.LocalFile = true
			report.LocalPath = clip.LocalPath()
		} else {
			report.LocalFile = false
			report.LocalPath = clip.LocalPath()
			report.LocalError = "file not found: " + statErr.Error()
			report.Issues = append(report.Issues, "local_file_missing")
		}
	} else {
		report.LocalFile = false
		report.Issues = append(report.Issues, "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
	}
	var fileID string
	if driveLink != "" {
		report.HasDriveLink = true
		report.DriveLink = driveLink
		fileID = ExtractDriveFolderID(driveLink)
		if fileID != "" {
			report.DriveFileID = fileID
			report.DriveLinkValid = true
		} else {
			report.DriveLinkValid = false
			report.Issues = append(report.Issues, "drive_link_invalid")
		}
	} else {
		report.HasDriveLink = false
		report.Issues = append(report.Issues, "drive_link_missing")
	}

	// Check hash
	//
	// S1c (June 2026) — verifyClip is now strictly read-only.
	// When the local clip row has no FileHash but Drive supplies a
	// matching MD5, the report exposes the candidate via
	// (HashRecoverable, HashRecoverableValue) but DOES NOT mutate
	// the row. The previous implementation called
	// `repo.Upsert(ctx, clip)` / `voiceoverRepo.Upsert` here — that
	// bypassed the asset-mutation isolation gate and could leave
	// the DB out-of-sync with the Qdrant point. Recovery is now an
	// explicit operator action (`POST /:source/clips/:id/fix-hash`,
	// Wave 22 task 5 / PR-CLIP-RAW-MUTATIONS follow-up), which
	// delegates to the dispatcher and emits the matching outbox
	// event for Qdrant replay.
	if clip.FileHash() != "" {
		report.Hash = clip.FileHash()
		report.HasHash = true
		if hasLocalFile {
			report.HashVerified = false
		}
	} else {
		if fileID != "" && s.driveUploader != nil {
			md5, err := s.driveUploader.GetFileMD5(ctx, fileID)
			if err == nil && md5 != "" {
				// READ-ONLY (S1c, June 2026): surface the candidate
				// hash in the report so operators can see "what Drive
				// would have us save" without us making the write
				// ourselves. The clip object passed in is NOT
				// mutated; this is a deliberate, observable signal
				// for split responsibility (verify = read; fix =
				// write).
				//
				// Field semantics on the read-only path (S1c):
				//   HashRecovered        = false (we did NOT write)
				//   HashRecoverable      = true  (drive has a candidate)
				//   HashRecoverableValue = md5   (the candidate)
				// The legacy HashRecovered field is preserved in
				// the struct schema for backwards-compat consumers
				// but is no longer set true on the read-only path.
				report.Hash = md5
				report.HasHash = true
				report.HashRecovered = false
				report.HashRecoverable = true
				report.HashRecoverableValue = md5
				// S1d (June 2026) — informational channel separation.
				// The "Drive has a candidate MD5 we could persist"
				// signal moves to the typed `HashInfo` block; the
				// `hash_recoverable_from_drive` slug is REMOVED from
				// `Issues[]` (the prior pattern meant a recoverable
				// clip flipped `Coherent` to false and bumped
				// `IssueCount`, painting dashboards red for a state
				// whose canonical remedy is the explicit operator
				// action POST /:source/clips/:id/fix-hash).
				report.HashInfo = HashInfo{
					Recoverable:   true,
					CandidateHash: md5,
				}
			} else {
				report.HasHash = false
				report.Issues = append(report.Issues, "hash_missing")
			}
		} else {
			report.HasHash = false
			report.Issues = append(report.Issues, "hash_missing")
		}
	}

	if clip.FolderID() != "" {
		report.FolderID = clip.FolderID()
	}
	if clip.FolderPath() != "" {
		report.FolderPath = clip.FolderPath()
	}

	status := "unknown"
	if clip.DriveLink() != "" || clip.DownloadLink() != "" {
		status = "processed"
	} else if clip.LocalPath() != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}
	report.Status = status

	if len(report.Issues) == 0 {
		report.Coherent = true
	} else {
		report.Coherent = false
		report.IssueCount = len(report.Issues)
	}

	return report
}

// FixHashReport mirrors the wire shape returned by HandleFixHash for
// HTTP callers. The application-side FixHash returns the same fields
// as struct scalars so the handler can JSON-marshal without reformat.
type FixHashReport struct {
	OK           bool
	Source       string
	ClipID       string
	PreviousHash string
	NewHash      string
	Reindexed    bool
	DispatcherOK bool
	Message      string
}

// FixHash orchestrates the fix-hash recovery flow:
//  1. resolve repo for `source`
//  2. reject voiceover source (it lives in a separate table that
//     AssetMutationDispatcher does not write to)
//  3. read clip from repo
//  4. extract Drive fileID from clip.DriveLink/DownloadLink
//  5. fetch MD5 from Drive
//  6. set FileHash on the clip
//  7. delegate to dispatcher.EnqueueAndIndex (the canonical SSOT
//     writer + outbox event emitter — QDRANT-002 PR7 route).
//
// Returns the typed FixHashReport on success. Returns typed errors so
// the caller (HTTP handler) can branch on
// errors.Is(err, ErrFixHashVoiceoverUnsupported) /
// errors.Is(err, ErrFixHashMissingDriveLink) /
// errors.Is(err, ErrFixHashDispatcherUnavailable) without parsing
// string messages.
//
// S1d (June 2026): PR-CLIP-RAW-MUTATIONS compliance. The previous
// fix-hash-style operations inlined `repo.Upsert(ctx, clip)` calls
// that bypassed outbox.Dispatcher — leaving Qdrant orphaned. The
// dispatcher is the ONLY canonical writer route; restore / fix-hash
// go through it. The service method is mirrored by
// `Handler.HandleFixHash` in the api layer for minimal-scope S1d
// (Wave 14 migration moves the call path here).
func (s *ClipOpsService) FixHash(ctx context.Context, source, clipID string) (*FixHashReport, error) {
	report := &FixHashReport{
		Source: source,
		ClipID: clipID,
	}

	// S1d: voiceover records live in voiceovers (separate table)
	// which AssetMutationDispatcher does not write to. Reject
	// unambiguously so dashboards do not see the dispatcher reject
	// a malformed clip via deep stacktrace.
	if strings.EqualFold(source, "voiceover") {
		return nil, ErrFixHashVoiceoverUnsupported
	}

	repo := s.resolveRepo(source)
	if repo == nil {
		return nil, fmt.Errorf("fix-hash: invalid source: %q", source)
	}
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("fix-hash: read clip %q: %w", clipID, err)
	}
	report.PreviousHash = clip.FileHash()

	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
	}
	if driveLink == "" {
		return nil, ErrFixHashMissingDriveLink
	}
	fileID := ExtractDriveFolderID(driveLink)
	if fileID == "" {
		return nil, fmt.Errorf("fix-hash: drive_link %q has no extractable file id", driveLink)
	}
	if s.driveUploader == nil {
		return nil, fmt.Errorf("fix-hash: drive_uploader not wired")
	}
	md5, err := s.driveUploader.GetFileMD5(ctx, fileID)
	if err != nil || md5 == "" {
		return nil, fmt.Errorf("fix-hash: drive GetFileMD5(%s): %w", fileID, err)
	}
	clip.SetFileHash(md5)
	report.NewHash = md5

	if s.dispatcher == nil {
		report.OK = false
		report.Message = "dispatcher not wired; clip mutation NOT persisted"
		return report, ErrFixHashDispatcherUnavailable
	}
	if err := s.dispatcher.EnqueueAndIndex(ctx, clip, md5); err != nil {
		report.OK = false
		report.Message = fmt.Sprintf("dispatcher reject: %v", err)
		return report, fmt.Errorf("fix-hash: dispatcher.EnqueueAndIndex: %w", err)
	}
	report.OK = true
	report.Reindexed = true
	report.DispatcherOK = true
	report.Message = "fix-hash applied (outbox event emitted; clip sees re-index)"
	return report, nil
}

// resolveRepo looks up the canonical repo for a source via the
// SourceResolverPort. Returns nil if the source is unknown.
func (s *ClipOpsService) resolveRepo(source string) ClipRepositoryPort {
	if s.sourceResolver == nil {
		return nil
	}
	return s.sourceResolver.ResolveRepo(source)
}

// voiceoverDTOToClip inverts the projection from voiceover DTO into
// the canonical *asset.Asset the verifier expects.
func voiceoverDTOToClip(rec *ClipVoiceoverRecordDTO) *asset.Asset {
	if rec == nil {
		return nil
	}
	clip := &asset.Asset{
		ID:     rec.ID,
		Name:   rec.Filename,
		Source: asset.Source("voiceover"),
	}
	clip.SetLocalPath(rec.LocalPath)
	clip.SetDriveLink(rec.DriveLink)
	clip.SetDownloadLink(rec.DownloadLink)
	clip.SetDriveFileID(rec.DriveFileID)
	clip.SetFolderID(rec.FolderID)
	clip.SetFolderPath(rec.FolderPath)
	clip.SetFileHash(rec.FileHash)
	return clip
}
