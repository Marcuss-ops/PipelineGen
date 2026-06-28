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

// ── Typed error sentinels for the fix-hash flow (S1d, June 2026) ───────
//
// Pair with errors.Is for branching on caller-side. The handler layer
// (api/assets/clips/clip_ops.go::HandleFixHash) translates each sentinel
// into the canonical HTTP status code:
//
//	ErrFixHashVoiceoverUnsupported   → 400 (unsupported source)
//	ErrFixHashMissingDriveLink       → 409 (clip has no Drive mirror)
//	ErrFixHashDispatcherUnavailable  → 503 (dispatcher not wired)
//
// Other errors (decode / repo read / Drive API) fall through as
// 500 Internal Error with a typed wrap.
// ── Typed error sentinels for the fix-hash flow (S1d, June 2026) ───────
//
// Pair with errors.Is for branching on caller-side. The handler layer
// (api/assets/clips/clip_ops.go::HandleFixHash) translates each sentinel
// into the canonical HTTP status code:
//
//	ErrFixHashVoiceoverUnsupported   → 400 (unsupported source)
//	ErrFixHashMissingDriveLink       → 409 (clip has no Drive mirror)
//	ErrFixHashDispatcherUnavailable  → 503 (dispatcher not wired)
//
// Wave 22 PR-5 polish (June 2026) adds:
//
//	ErrJobsUnavailable               → 503 (jobs service not wired)
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

	// ErrInvalidSource is the typed sentinel returned by Cleanup
	// when the source parameter is not a canonical cleanup
	// source: neither a static global scope (all / voiceover /
	// images) nor a provider-registered source resolvable via
	// s.sourceResolver. Cleanup returns this with a wrapped
	// reason (via fmt.Errorf("%w: %s", ErrInvalidSource,
	// in.Source)) so the HTTP layer (api/assets/clips/clip_ops.go
	// ::mapClipOpsError) can map it to 400 Bad Request via
	// errors.Is. Source validation runs BEFORE the jobs-nil
	// check so callers with bad input see a clean 400 instead
	// of a misleading 503 (composition-bug signal reserved for
	// callers who passed valid input but have no broker wired).
	ErrInvalidSource = errors.New("invalid cleanup source")

	// ErrReconcileQueueUnavailable is the typed sentinel returned by
	// Reconcile when s.jobs is nil OR the broker-side enqueue fails.
	// PR-3 (June 2026): the previous log-only stub has been removed;
	// every Reconcile call must enqueue a durable catalog.sync job.
	// The HTTP handler maps this to
	//   503 Service Unavailable + body {"ok": false,
	//   "error": "RECONCILE_QUEUE_UNAVAILABLE", ...}
	// so callers can detect "broker missing" distinctly from
	// "transient broker reject" (the latter surfaces with a wrapped
	// error from the underlying enqueue call).
	//
	// Prefix "RECONCILE_QUEUE_UNAVAILABLE:" is canonical so log
	// greps + JSON-shape assertions can detect it without parsing
	// the human-readable message.
	ErrReconcileQueueUnavailable = errors.New("RECONCILE_QUEUE_UNAVAILABLE: reconcile requires jobs service (catalog.sync broker missing)")
)

// ── Reconcile result (PR-3, June 2026) ──────────────────────────────
//
// ReconcileResult is the typed reply of Reconcile. JobID is the
// canonical catalog.sync broker-assigned id; callers poll
// GetJobStatus(JobID) to track progress. Field kept minimal —
// clients that need more detail pivot on GetJobStatus + the
// full job record.
type ReconcileResult struct {
	JobID string
}

// (CleanupServicePort + JobsServicePort live below as interfaces that the
// composition root will adapt to *deletion.DeletionService and
// domain/job.Service. We deliberately do NOT import either internal-package
// type here — api/clips/handler.go and the composition root remain the
// only places that bridge the canonical concrete services into these
// narrow ports.)

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

// Reconcile reconciles database with Drive files via a real
// catalog.sync job. PR-3 (June 2026): the previous log-only stub
// has been removed — every Reconcile call now enqueues a durable
// catalog.sync job that a worker consumes from the broker pool.
// Fail-closed: every non-success path returns the typed sentinel
// ErrReconcileQueueUnavailable so the HTTP handler can map it to
// 503 + RECONCILE_QUEUE_UNAVAILABLE instead of presenting a fake
// "ok" response. Specifically:
//   - s.jobs is nil (broker port never wired) → sentinel
//   - s.jobs.Enqueue returns ANY error (broker reachable but
//     rejects — queue full, dispatcher down, malformed payload,
//     context cancelled against the broker, etc.) → sentinel
//
// The user's fail-closed contract is "broker non disponibile →
// 503 invece di mentire". Both "not wired" and "rejecting" count
// as "broker unavailable" from the caller's perspective: a 500
// would force clients to retry on the same idempotency they used
// for the original call, while a 503 lets clients treat both as a
// transient infrastructure signal and back off. The wrapped error
// still carries the underlying broker message for operator log
// forensics.
//
// Payload shape: matches the canonical
// `internal/application/jobs/payloads.CatalogSyncPayload{}` JSON
// fields verbatim (Source, FolderID, ForceFull — see
// Payload: `source` JSON key, `Payload: `folder_id` JSON key,
// `Payload: `force_full` JSON key). The composition-root adapter
// converts the map[string]any into the typed payload on the way
// into the broker, so the consumer
// `catalogsync.Service.HandleJob` unmarshals it as the canonical
// struct. Field naming MUST stay in lockstep with
// application/jobs/payloads.go::CatalogSyncPayload — drift here
// surfaces as a silent zero-value payload on the worker side.
//
// ActiveKey is empty: each Reconcile call produces a distinct
// broker job_id even when source+folderID repeat. Reconciling the
// same folder twice SHOULD land twice in the broker — duplicates
// here are operator intent, not a deduplication surface. The folder
// scope travels in the payload, not in ActiveKey.
func (s *ClipOpsService) Reconcile(ctx context.Context, source, folderID string) (*ReconcileResult, error) {
	if s.jobs == nil {
		return nil, ErrReconcileQueueUnavailable
	}
	resp, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type: job.TypeCatalogSync,
		Payload: map[string]any{
			"source":     source,
			"folder_id":  folderID,
			"force_full": true,
		},
		Priority: 5,
	})
	if err != nil {
		if s.log != nil {
			s.log.Error("reconcile: enqueue catalog.sync failed (broker unreachable / rejected)",
				zap.String("source", source),
				zap.String("folder_id", folderID),
				zap.Error(err))
		}
		return nil, fmt.Errorf("%w: %v", ErrReconcileQueueUnavailable, err)
	}
	if s.log != nil {
		s.log.Info("reconcile: catalog.sync job enqueued",
			zap.String("source", source),
			zap.String("folder_id", folderID),
			zap.String("job_id", resp.ID))
	}
	return &ReconcileResult{JobID: resp.ID}, nil
}

// CleanupInput captures the request shape for Cleanup. The HTTP
// method on the api side ShouldBindJSON's this directly.
type CleanupInput struct {
	Source     string
	DryRun     bool
	CheckDrive bool
	Deep       bool
}

// CleanupReport is the JSON shape returned to the caller. Mirrors
// the legacy api-output keys verbatim so existing clients don't see
// drift.
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

// CleanupItem is a per-clip row in the report.
type CleanupItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Cleanup orchestrates orphan-record cleanup.
//
// S1b (June 2026): removed the synchronous 10000-record inline
// pagination path. Cleanup always enqueues a job through
// JobsServicePort — the worker does the actual orphan scan,
// Drive-files.Get, and physical delete from the broker pool. See
// `docs/archive/migration-history.md` for the full rationale and
// the AGENTS.md Pattern 8 reference.
//
// Wave 22 PR-5 polish (June 2026): Cleanup returns the typed
// sentinel ErrJobsUnavailable when s.jobs is nil (test fixtures
// or partial deployments) so the api handler can map it to 503
// via errors.Is. The job type is also canonicalised to
// job.TypeSystemCleanup (was the literal "system.cleanup").
func (s *ClipOpsService) Cleanup(ctx context.Context, in CleanupInput) (*CleanupReport, error) {
	src := strings.ToLower(strings.TrimSpace(in.Source))
	if !s.isKnownCleanupSource(src) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidSource, in.Source)
	}
	if s.jobs == nil {
		return nil, ErrJobsUnavailable
	}

	deep := in.Deep
	report := &CleanupReport{
		OK:         true,
		Source:     src, // normalized so report.Source == payload["source"] (worker routing key alignment)
		DryRun:     in.DryRun,
		CheckDrive: in.CheckDrive,
		Items:      []CleanupItem{},
	}

	activeKey := "system_maintenance_manual"
	if in.DryRun {
		activeKey += "_dry"
	}
	if deep {
		activeKey += "_deep"
	}

	// The composition-root adapter converts our minimal DTO into
	// the canonical *domain/job.EnqueueRequest shape; this keeps
	// ports.go zero-infra per the canonical port-segregation rule.
	job, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type: job.TypeSystemCleanup,
		Payload: map[string]any{
			"deep":        deep,
			"dry_run":     in.DryRun,
			"check_drive": in.CheckDrive,
			"source":      src, // normalized so worker receives payload["source"] matching resolver-registered keys (case-insensitive dependency)
		},
		Priority:  10,
		ActiveKey: activeKey,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue cleanup job: %w", err)
	}
	report.JobID = job.ID
	report.Message = fmt.Sprintf("system cleanup job enqueued; poll job_id=%s for results", job.ID)
	report.Summary = fmt.Sprintf("enqueued (job_id=%s)", job.ID)
	// S1b keeps the legacy CleanupItem slice in the report so old
	// callers that iterate the union shape continue to render
	// without null-deref. New callers pivot on JobID.
	report.Items = []CleanupItem{}
	return report, nil
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
// on two parallel surfaces (the names mirror each other):
//   - api-side:    Handler.HandleFixHash -> response {"reindexed": true}
//   - service-side: FixHashReport.{OK, Reindexed, DispatcherOK}
//
// Both intentionally live on FixHashReport (not HashInfo) so
// HashInfo remains the strictly informational channel.
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

// isKnownCleanupSource returns true when src (already
// lowercase-normalized by the caller) matches one of the
// canonical static global cleanup scopes or resolves via
// s.sourceResolver to a registered clip repo. This is the
// single source of truth for "what's a valid cleanup source";
// the HTTP layer relies on errors.Is(err, ErrInvalidSource)
// to translate the negative answer into a 400 Bad Request.
//
// Static scopes (per AGENTS.md + pre-PR-3 handler contract):
//   - "all"      — wildcard (cleanup every source)
//   - "voiceover" — voiceover-source cleanup (separate voiceovers table;
//     AssetMutationDispatcher does not write to it)
//   - "images"   — images-source cleanup
//
// Dynamic scopes (provider-registered: youtube / artlist /
// stock / etc.) come from s.sourceResolver.ResolveRepo(src).
// Co-existence with the resolver delegate prevents hardcoding
// the full provider register while still keeping the
// global-scope validation self-contained.
func (s *ClipOpsService) isKnownCleanupSource(src string) bool {
	switch src {
	case "all", "voiceover", "images":
		return true
	}
	return s.sourceResolver != nil && s.sourceResolver.ResolveRepo(src) != nil
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
