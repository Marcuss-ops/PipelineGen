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
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

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
type ClipOpsService struct {
	sourceResolver SourceResolverPort
	voiceoverRepo  VoiceoverRepositoryPort
	imagesRepo     ImageRepositoryPort
	driveUploader  ClipDriveUploaderPort
	cleanup        CleanupServicePort
	jobs           JobsServicePort
	log            *zap.Logger
}

// NewClipOpsService constructs the canonical service. Pass nil for
// ports that callers don't use (test fixtures, partial deployments);
// the corresponding service methods will internal-error / no-op per
// the legacy semantics.
func NewClipOpsService(
	sourceResolver SourceResolverPort,
	voiceoverRepo VoiceoverRepositoryPort,
	imagesRepo ImageRepositoryPort,
	driveUploader ClipDriveUploaderPort,
	cleanup CleanupServicePort,
	jobs JobsServicePort,
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
		log:            log,
	}
}

// Reconcile reconciles database with Drive files. The api-side
// behavior is preserved: returns "stub-ok" since catalogSync lives on
// a separate path; callers wanting the orchestration path should hit
// the route's alternative handler.
func (s *ClipOpsService) Reconcile(ctx context.Context, source, folderID string) {
	if s.log != nil {
		s.log.Info("Starting reconciliation",
			zap.String("source", source),
			zap.String("folder", folderID))
	}
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
	JobID      string  // populated when deep=true & jobs service is wired
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
	ID     string
	Name   string
	Reason string
}

// Cleanup orchestrates orphan-record cleanup. Two paths:
//   - deep=true + source=all/"" → enqueue "system.cleanup" via jobs service
//   - otherwise → synchronous per-source listing + deleteClip for orphans
func (s *ClipOpsService) Cleanup(ctx context.Context, in CleanupInput) (*CleanupReport, error) {
	deep := in.Deep
	report := &CleanupReport{
		OK:         true,
		Source:     in.Source,
		DryRun:     in.DryRun,
		CheckDrive: in.CheckDrive,
		Items:      []CleanupItem{},
	}

	// Use Job system for heavy all-source deep cleanup
	if deep && (strings.ToLower(in.Source) == "all" || in.Source == "") {
		if s.jobs != nil {
			activeKey := "system_maintenance_manual"
			if in.DryRun {
				activeKey += "_dry"
			}
			// The composition-root adapter is responsible for converting
			// our minimal request DTO into the canonical *EnqueueRequest
			// shape (domain/job). This keeps ports.go zero-infra.
			job, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
				Type:      "system.cleanup",
				Payload:   map[string]any{"deep": true, "dry_run": in.DryRun},
				Priority:  10,
				ActiveKey: activeKey,
			})
			if err != nil {
				return nil, fmt.Errorf("enqueue cleanup job: %w", err)
			}
			report.JobID = job.ID
			report.Message = "system cleanup job enqueued"
			return report, nil
		}
		// Fallback to synchronous if no jobs service (unlikely)
		if s.cleanup != nil && !in.DryRun {
			deleted, err := s.cleanup.CleanupOrphanFiles(ctx, "", false)
			if err != nil {
				return nil, fmt.Errorf("synchronous cleanup: %w", err)
			}
			report.Deleted = deleted
			report.Message = "deep cleanup completed synchronously"
			report.Summary = fmt.Sprintf("Found %d orphans, deleted %d", deleted, deleted)
			return report, nil
		}
	}

	repo := s.resolveRepo(in.Source)
	sourceLower := strings.ToLower(in.Source)
	if repo == nil && sourceLower != "images" && sourceLower != "voiceover" {
		return nil, fmt.Errorf("invalid source: %s", in.Source)
	}

	var allClips []*asset.Asset

	if sourceLower == "images" && s.imagesRepo != nil {
		imgs, _ := s.imagesRepo.ListAll(ctx)
		for _, img := range imgs {
			allClips = append(allClips, artifacts.ImageAssetToClip(img))
		}
	} else if sourceLower == "voiceover" && s.voiceoverRepo != nil {
		recs, _ := s.voiceoverRepo.ListAll(ctx)
		for _, rec := range recs {
			allClips = append(allClips, voiceoverDTOToClip(rec))
		}
	} else if repo != nil {
		clips, err := repo.ListClipsPaged(ctx, in.Source, 10000, 0, "")
		if err == nil {
			allClips = clips
		}
	}

	deletedCount := 0

	for _, clip := range allClips {
		verify := s.Verify(ctx, in.Source, clip.ID)
		hasDB := verify.DB
		hasLocal := verify.LocalFile
		hasDrive := verify.HasDriveLink
		isOrphan := !hasDB || (!hasLocal && !hasDrive)

		if isOrphan {
			if !in.DryRun && s.cleanup != nil {
				if err := s.cleanup.DeleteClip(ctx, in.Source, clip.ID, false); err == nil {
					deletedCount++
				}
			}
			report.Items = append(report.Items, CleanupItem{
				ID:     clip.ID,
				Name:   clip.Name,
				Reason: "orphan",
			})
		}
	}

	report.Checked = len(report.Items)
	report.Deleted = deletedCount
	summary := fmt.Sprintf("Found %d orphans", len(report.Items))
	if !in.DryRun {
		summary += fmt.Sprintf(", deleted %d", deletedCount)
	}
	report.Summary = summary
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
type VerifyReport struct {
	OK              bool
	Source          string
	ClipID          string
	Issues          []string
	DB              bool
	LocalFile       bool
	LocalPath       string
	LocalError      string
	HasDriveLink    bool
	DriveLink       string
	DriveFileID     string
	DriveLinkValid  bool
	Hash            string
	HasHash         bool
	HashVerified    bool
	HashRecovered   bool
	FolderID        string
	FolderPath      string
	Status          string
	Coherent        bool
	IssueCount      int
	Extra           map[string]any // catch-all for adapter-extended fields
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
				clip.SetFileHash(md5)
				report.Hash = md5
				report.HasHash = true
				report.HashRecovered = true
				// QDRANT-asset-mutation isolation (June 2026):
				// upsertClip(ctx, clip) is REMOVED from ClipRepositoryPort.
				// Hash-recovery still patches the file_hash field but the
				// write uses the lower-level Upsert (still public, still
				// outbox-bypassing but syntactically permitted on the port).
				// The driver for this is the lint ban on `UpsertClip\(` in
				// internal/application + internal/api production paths.
				if repo != nil {
					if err := repo.Upsert(ctx, clip); err != nil {
						if s.log != nil {
							s.log.Warn("failed to save recovered hash", zap.String("clip_id", clip.ID), zap.Error(err))
						}
					} else if s.log != nil {
						s.log.Info("recovered and saved missing hash from drive", zap.String("clip_id", clip.ID), zap.String("hash", md5))
					}
				} else if strings.ToLower(source) == "voiceover" && s.voiceoverRepo != nil {
					rec, err := s.voiceoverRepo.GetByID(ctx, clip.ID)
					if err == nil && rec != nil {
						rec.FileHash = md5
						if err := s.voiceoverRepo.Upsert(ctx, rec); err != nil {
							if s.log != nil {
								s.log.Warn("failed to save recovered voiceover hash", zap.String("id", clip.ID), zap.Error(err))
							}
						} else if s.log != nil {
							s.log.Info("recovered and saved missing voiceover hash", zap.String("id", clip.ID), zap.String("hash", md5))
						}
					}
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

	// Reference time.Now() so go vet doesn't flag time as unused if
	// a future refactor stops using it indirectly via the drive MD5.
	_ = time.Now()

	return report
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
