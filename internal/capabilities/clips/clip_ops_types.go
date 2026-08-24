package clips

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Reconcile result (PR-3, June 2026) ──────────────────────────────

// ReconcileResult is the typed reply of Reconcile. JobID is the
// canonical catalog.sync broker-assigned id; callers poll
// GetJobStatus(JobID) to track progress.
type ReconcileResult struct {
	JobID string
}

// ── ClipOps service ──────────────────────────────────────────────────

// ClipOpsService owns the orchestration behind the HTTP verbs
// Reconcile / Cleanup / VerifyClip. Construction via NewClipOpsService;
// every required port is passed in.
//
// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the `sourceResolver`
// field is REMOVED — all clip-type sources share a single canonical
// repo, and the per-source discriminator moves to the query layer
// rather than a runtime port-swap. The new `clipRepo` field holds
// the canonical ClipRepositoryPort the service uses for all sources.
//
// S1d (June 2026): the `dispatcher` field is added so the service can
// route fix-hash recovery through the canonical AssetMutationDispatcher port.
type ClipOpsService struct {
	clipRepo      ClipRepositoryPort
	voiceoverRepo VoiceoverRepositoryPort
	imagesRepo    ImageRepositoryPort
	driveUploader ClipDriveUploaderPort
	jobs          JobsServicePort
	dispatcher    ClipIndexDispatcherPort
	log           *zap.Logger
}

// NewClipOpsService constructs the canonical service. Pass nil for
// ports that callers don't use (test fixtures, partial deployments);
// the corresponding service methods will internal-error / no-op per
// the legacy semantics.
//
// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): clipRepo replaces the
// retired sourceResolver as the canonical repo source — the service
// queries it directly per-call rather than swapping repos at runtime.
func NewClipOpsService(
	clipRepo ClipRepositoryPort,
	voiceoverRepo VoiceoverRepositoryPort,
	imagesRepo ImageRepositoryPort,
	driveUploader ClipDriveUploaderPort,
	jobs JobsServicePort,
	dispatcher ClipIndexDispatcherPort,
	log *zap.Logger,
) *ClipOpsService {
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipOpsService{
		clipRepo:      clipRepo,
		voiceoverRepo: voiceoverRepo,
		imagesRepo:    imagesRepo,
		driveUploader: driveUploader,
		jobs:          jobs,
		dispatcher:    dispatcher,
		log:           log,
	}
}

// ── Cleanup DTOs ────────────────────────────────────────────────────

// CleanupInput captures the request shape for Cleanup.
type CleanupInput struct {
	Source     string
	DryRun     bool
	CheckDrive bool
	Deep       bool
}

// CleanupReport is the JSON shape returned to the caller.
type CleanupReport struct {
	OK         bool
	Source     string
	JobID      string
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

// ── Verify DTOs ─────────────────────────────────────────────────────

// VerifyReport mirrors the pre-PR2 api-output keys for VerifyClip.
//
// S1c (June 2026) — VerifyClip is now strictly read-only.
// The legacy flat fields (HashRecovered, HashRecoverable,
// HashRecoverableValue) are preserved for back-compat JSON consumers
// but new consumers should read HashInfo for the canonical typed shape.
type VerifyReport struct {
	OK     bool
	Source string
	ClipID string
	// Issues carries BLOCKING issues only. Informational signals
	// (the canonical example: "Drive has a recoverable MD5 we could
	// persist") live in their own typed fields (HashInfo).
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
	// HashInfo is the S1d typed informational channel for the
	// "Drive has a candidate MD5 we could persist" signal.
	HashInfo HashInfo
	// Legacy flat fields — JSON back-compat ONLY. Read HashInfo for canonical semantics.
	HashRecovered        bool
	HashRecoverable      bool
	HashRecoverableValue string
	FolderID             string
	FolderPath           string
	Status               string
	Coherent             bool
	IssueCount           int
	Extra                map[string]any
}

// HashInfo is the S1d (June 2026) typed informational channel for
// the "Drive supplies a candidate MD5 we could persist" case.
//
// CRITICAL CONTRACT (S1d): the verify read-only path MUST NOT
// append the slug "hash_recoverable_from_drive" to
// VerifyReport.Issues[] even though the recoverable signal IS
// present.
type HashInfo struct {
	Recoverable   bool
	CandidateHash string
}

// ── FixHash DTO ─────────────────────────────────────────────────────

// FixHashReport mirrors the wire shape returned by HandleFixHash for
// HTTP callers.
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

// ── Helpers ──────────────────────────────────────────────────────────

// resolveRepo returns the canonical clip repo for any clip-type source.
//
// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the retired
// SourceResolverPort layer is GONE — all clip-type sources share the
// same canonical ClipRepositoryPort. The source discriminator now
// lives at the QUERY layer (the repo methods take `source` as a
// per-call filter parameter), not at port-selection time. The
// signature is preserved (still takes source + returns the repo)
// so existing callers stay compile-equivalent during the wave.
func (s *ClipOpsService) resolveRepo(source string) ClipRepositoryPort {
	_ = source // kept for caller-compat; the per-source discriminator is canonical at the query layer
	return s.clipRepo
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
	clip.SetLegacyFileMD5(rec.LegacyFileMD5)
	return clip
}
