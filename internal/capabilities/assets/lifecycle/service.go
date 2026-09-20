package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

var (
	ErrFinalizerUnavailable      = errors.New("asset lifecycle: finalizer unavailable")
	ErrAssetStoreUnavailable     = errors.New("asset lifecycle: asset store unavailable")
	ErrReconcilerUnavailable     = errors.New("asset lifecycle: reconciler unavailable")
	ErrDrivePublisherUnavailable = errors.New("asset lifecycle: Drive publisher unavailable")
	ErrDriveUploadFailed         = errors.New("asset lifecycle: Drive upload failed")
	ErrFinalizationFailed        = errors.New("asset lifecycle: finalization failed")
)

type Service struct {
	store         AssetRecordStore
	dedupe        *assetop.DedupeService
	reconcile     *assetop.ReconcileService
	publisher     delivery.Publisher
	driveReader   drive.Reader
	finalizer     Finalizer
	uploadPolicy  assetop.UploadPolicy
	persistPolicy assetop.PersistPolicy
	registry      artifacts.Registry
	assetIndex    *assetindex.Service
	log           *zap.Logger
}

type Config struct {
	DuplicatePolicy assetop.DuplicatePolicy
	UploadPolicy    assetop.UploadPolicy
	PersistPolicy   assetop.PersistPolicy
	ReconcilePolicy assetop.ReconcilePolicy
}

type ServiceDeps struct {
	Store       AssetRecordStore
	Publisher   delivery.Publisher
	DriveReader drive.Reader
	Registry    artifacts.Registry
	AssetIndex  *assetindex.Service
	Finalizer   Finalizer
	Log         *zap.Logger
}

func NewService(deps ServiceDeps, cfg Config) *Service {
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	dedupe := assetop.NewDedupeService(deps.Store, cfg.DuplicatePolicy, deps.Log)
	var reconcile *assetop.ReconcileService
	if cfg.ReconcilePolicy.Enabled && deps.DriveReader != nil {
		reconcile = assetop.NewReconcileService(deps.Store, deps.DriveReader, cfg.ReconcilePolicy, deps.Log)
	}
	return &Service{store: deps.Store, dedupe: dedupe, reconcile: reconcile, publisher: deps.Publisher, driveReader: deps.DriveReader, finalizer: deps.Finalizer, uploadPolicy: cfg.UploadPolicy, persistPolicy: cfg.PersistPolicy, registry: deps.Registry, assetIndex: deps.AssetIndex, log: deps.Log}
}

// byteIdentity returns the canonical SHA-256 of the artifact bytes plus the
// exact byte count that produced it.
//
// The ok flag distinguishes "the bytes could not be read" from "the artifact
// has no local bytes". Callers MUST keep those apart: an unreadable artifact is
// a verification failure, while a reference-only record is legitimately
// address-less. Collapsing them is how an empty verification signal gets
// accepted for bytes that were supposed to be checked.
func byteIdentity(localPath string) (sum string, sizeBytes int64, ok bool) {
	if strings.TrimSpace(localPath) == "" {
		return "", 0, false
	}
	sum, size, err := digest.SHA256File(localPath)
	if err != nil {
		return "", 0, false
	}
	return sum, size, true
}

// canonicalCallerHash returns the caller-supplied digest when it already IS a
// canonical SHA-256, and the empty string otherwise. An MD5 is never promoted
// into a content address.
func canonicalCallerHash(fileHash string) string {
	if digest.IsCanonicalSHA256(fileHash) {
		return strings.ToLower(strings.TrimSpace(fileHash))
	}
	return ""
}

// contentAddress returns the canonical byte identity (SHA-256) of the artifact
// about to be recorded, or the empty string when it cannot be established.
//
// It is deliberately NOT the caller's fileHash. That value is the legacy MD5
// the ingest and YouTube callers compute (checksum.LegacyMD5File) and it is
// carried for compatibility only: an MD5 can never be the durable content
// address of the media SSOT, because every reader of that column treats it as a
// SHA-256 (kernel/asset.Ref.SHA256, digest.IsCanonicalSHA256, the Drive
// uploader's ExpectedSHA256). Recording one there is a lie that only surfaces
// later, in a different process, as "the hash does not match the bytes" — the
// exact failure the media-identity programme exists to remove.
//
// The bytes are the only source of truth, so the address is computed from them
// whenever the artifact is readable. When it is not (a caller that records a
// reference without local bytes), the address is left EMPTY unless the caller
// already handed over a canonical SHA-256: an unknown identity is honest, a
// wrong one is not.
//
// LegacyFileMD5 keeps the caller's value untouched: it is a read-only
// compatibility bucket and no decision may read it.

// ProcessAsset makes the canonical SQLite-owned record before any Drive side
// effect. The first commit records PUBLISH_PENDING; the second records either
// PUBLISHED or PUBLISH_FAILED so failed delivery remains recoverable.
func (s *Service) ProcessAsset(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: input is required", ErrFinalizationFailed)
	}
	out := &FinalizeResult{LocalPath: input.LocalPath, DriveLink: input.DriveLink, DriveFileID: input.DriveFileID, DownloadLink: input.DownloadLink, LegacyFileMD5: fileHash}

	// BYTE IDENTITY (media-identity programme, Sept 2026). The content address
	// is the SHA-256 of the artifact's bytes and the size is the exact byte
	// count that produced it. Both are derived from the bytes — never from the
	// caller's legacy MD5 — because the same pair is what the Drive uploader
	// verifies against AFTER the upload.
	contentHash, sizeBytes, bytesKnown := byteIdentity(input.LocalPath)
	if !bytesKnown {
		// No readable local bytes: the identity is whatever the caller could
		// legitimately prove (a canonical SHA-256), else honestly UNKNOWN.
		contentHash = canonicalCallerHash(fileHash)
	}
	out.ContentHash = contentHash

	if input.RequireDrive && input.LocalPath == "" {
		return out, fmt.Errorf("%w: local path is required", ErrDriveUploadFailed)
	}
	needsDelivery := input.RequireDrive || (s.uploadPolicy.Enabled && input.LocalPath != "")
	if needsDelivery && !s.persistPolicy.SaveToAssetRegistry {
		return out, fmt.Errorf("%w: Drive delivery requires canonical persistence", ErrFinalizerUnavailable)
	}
	if s.persistPolicy.SaveToAssetRegistry && s.finalizer == nil {
		return nil, ErrFinalizerUnavailable
	}

	// FAIL-CLOSED VERIFICATION PREFLIGHT (Fix D). A publish whose local bytes
	// exist must carry a REAL verification signal: if the bytes cannot be read
	// we cannot state a SHA-256 or a size, and an empty verification would let
	// the uploader skip the post-upload size+checksum gate silently. There is
	// no zero, no empty string and no MD5 fallback here — the publish fails.
	if needsDelivery && strings.TrimSpace(input.LocalPath) != "" {
		if !bytesKnown {
			return out, fmt.Errorf("%w: content identity unavailable: local bytes at %q are unreadable (refusing an unverifiable upload)", ErrDriveUploadFailed, input.LocalPath)
		}
		if contentHash == "" {
			return out, fmt.Errorf("%w: content identity unavailable: no SHA-256 for %q (refusing an unverifiable upload)", ErrDriveUploadFailed, input.LocalPath)
		}
		if sizeBytes <= 0 {
			return out, fmt.Errorf("%w: byte size unavailable for %q (refusing an unverifiable upload)", ErrDriveUploadFailed, input.LocalPath)
		}
	}

	// DEDUP DECISION (Fix C). The asset ID answers "which asset is this"; the
	// SHA-256 answers "which bytes are these". They are resolved separately:
	//
	//	same ID + same bytes        → idempotent skip
	//	same ID + changed bytes     → conflict/replacement, NOT a duplicate
	//	different ID + same bytes   → new logical asset reusing the storage
	//	different ID + new bytes    → new asset + new blob
	//
	// The legacy MD5 is never consulted: it is compatibility-only and an MD5 is
	// not a content address.
	if s.dedupe != nil && s.dedupe.Policy().Enabled {
		if s.store == nil {
			return out, ErrAssetStoreUnavailable
		}
		match, err := s.dedupe.CheckDuplicate(ctx, assetop.ExistingAssetQuery{
			ID: input.ID, ContentSHA256: contentHash,
			DriveFileID: input.DriveFileID, Filename: input.Filename, Source: input.Source,
		})
		if err != nil {
			return out, fmt.Errorf("%w: duplicate check: %w", ErrFinalizationFailed, err)
		}
		switch {
		case match != nil && match.Kind == assetop.DuplicateSameAsset && match.SameContent && s.dedupe.Policy().SkipIfExists:
			existing := match.Record
			out.OK, out.Status, out.DeliveryStatus = true, "skipped_duplicate", asset.AssetPublishPublished
			out.DriveLink, out.DriveFileID, out.DownloadLink, out.LegacyFileMD5 = existing.DriveLink, existing.DriveFileID, existing.DownloadLink, existing.LegacyFileMD5
			out.ContentHash = contentHash
			return out, nil
		case match != nil && match.Kind == assetop.DuplicateSameContent && match.Record != nil:
			// These bytes are already stored under ANOTHER logical asset. The
			// record is still created: the two assets share one physical
			// content identity (same ContentHash, therefore the same blob),
			// which is the point of content addressing. We do NOT relink the
			// Drive identity here — a Drive file lives in the folder layout of
			// the asset that published it, so pointing a second asset at it
			// would report a success whose bytes are not in that asset's
			// folder. Reusing the physical blob is the storage layer's job.
			out.ReusedAssetID = match.Record.ID
		}
	}

	driveLink, driveFileID, downloadLink := input.DriveLink, input.DriveFileID, input.DownloadLink
	publishStatus := asset.AssetPublishLocalOnly
	rec := &artifacts.MediaRecord{ID: input.ID, Name: input.Name, Filename: input.Filename, Source: input.Source, MediaType: string(input.Kind), FolderID: input.FolderID, FolderPath: input.FolderPath, Group: input.Group, LocalPath: input.LocalPath, DriveLink: driveLink, DriveFileID: driveFileID, DownloadLink: downloadLink, LegacyFileMD5: fileHash, ContentHash: contentHash, Metadata: input.Metadata, Status: "delivery_pending", PublishStatus: asset.AssetPublishPending, Duration: input.Duration, SourceID: input.SourceID, Subfolder: input.Subfolder}

	if needsDelivery {
		if _, err := s.commitRecord(ctx, rec, false); err != nil {
			return out, fmt.Errorf("%w: pending commit: %w", ErrFinalizationFailed, err)
		}
	}

	if needsDelivery {
		if s.publisher == nil {
			publishStatus = asset.AssetPublishFailed
			rec.PublishStatus, rec.Error = publishStatus, ErrDrivePublisherUnavailable.Error()
			if _, err := s.commitRecord(ctx, rec, false); err != nil {
				return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
			}
			if input.RequireDrive {
				return out, ErrDrivePublisherUnavailable
			}
		} else {
			filename := input.Filename
			if filename == "" {
				filename = filepath.Base(input.LocalPath)
			}
			// The verification signals ride the request so the publisher threads
			// them into PutFileRequest.ExpectedSize/ExpectedSHA256 and the
			// post-upload verifier rejects a Drive file whose size or SHA-256
			// does not match the bytes we hashed. Fail-closed: the preflight
			// above guarantees both are known and non-zero on this path.
			pubRes, pubErr := s.publisher.Publish(ctx, delivery.PublishRequest{Destination: input.Destination, LocalPath: input.LocalPath, Filename: filename, AssetID: input.ID, Group: input.Group, Subject: input.Subject, ProjectID: input.ProjectID, Language: input.Language, Style: input.Style, ContentHash: contentHash, SizeBytes: sizeBytes})
			if pubErr != nil || pubRes == nil {
				publishStatus = asset.AssetPublishFailed
				rec.PublishStatus = publishStatus
				if pubErr != nil {
					rec.Error = pubErr.Error()
				} else {
					rec.Error = "publisher returned nil result"
				}
				if _, err := s.commitRecord(ctx, rec, false); err != nil {
					return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
				}
				if input.RequireDrive {
					if pubErr != nil {
						return out, fmt.Errorf("%w: %w", ErrDriveUploadFailed, pubErr)
					}
					return out, fmt.Errorf("%w: publisher returned nil result", ErrDriveUploadFailed)
				}
			} else if pubRes.FileID == "" && pubRes.WebViewLink == "" {
				publishStatus = asset.AssetPublishFailed
				rec.PublishStatus = publishStatus
				rec.Error = "publisher returned no Drive file identity"
				if _, err := s.commitRecord(ctx, rec, false); err != nil {
					return out, fmt.Errorf("%w: recovery commit: %w", ErrFinalizationFailed, err)
				}
				if input.RequireDrive {
					return out, fmt.Errorf("%w: publisher returned no Drive file identity", ErrDriveUploadFailed)
				}
			} else {
				publishStatus = asset.AssetPublishPublished
				rec.PublishStatus = publishStatus
				driveLink, driveFileID = pubRes.WebViewLink, pubRes.FileID
				if driveLink == "" && driveFileID != "" {
					driveLink = "https://drive.google.com/file/d/" + driveFileID + "/view"
				}
				if pubRes.DownloadLink != "" {
					downloadLink = pubRes.DownloadLink
				} else if pubRes.FileID != "" {
					downloadLink = "https://drive.google.com/uc?id=" + pubRes.FileID
				}
			}
		}
	}

	if !needsDelivery {
		publishStatus = asset.AssetPublishLocalOnly
		rec.PublishStatus, rec.Status = publishStatus, "processed"
		if s.persistPolicy.SaveToAssetRegistry {
			if _, err := s.commitRecord(ctx, rec, false); err != nil {
				return out, fmt.Errorf("%w: commit: %w", ErrFinalizationFailed, err)
			}
		}
	} else if publishStatus == asset.AssetPublishPublished {
		rec.DriveLink, rec.DriveFileID, rec.DownloadLink, rec.Status = driveLink, driveFileID, downloadLink, "processed"
		if _, err := s.commitRecord(ctx, rec, true); err != nil {
			// Drive already owns the external side effect. Preserve its
			// identity and make one explicit recovery attempt so the
			// canonical record remains retryable instead of looking lost.
			terminalErr := err
			rec.PublishStatus = asset.AssetPublishFailed
			rec.Status = "delivery_pending"
			rec.Error = terminalErr.Error()
			if _, recoveryErr := s.commitRecord(ctx, rec, false); recoveryErr != nil {
				return out, fmt.Errorf("%w: terminal commit: %v; recovery commit: %w", ErrFinalizationFailed, terminalErr, recoveryErr)
			}
			return out, fmt.Errorf("%w: terminal commit: %w", ErrFinalizationFailed, terminalErr)
		}
	}

	out.OK, out.DeliveryStatus = true, publishStatus
	out.Status = "processed"
	if publishStatus == asset.AssetPublishFailed {
		out.Status = "delivery_pending"
	}
	out.DriveLink, out.DriveFileID, out.DownloadLink = driveLink, driveFileID, downloadLink
	return out, nil
}

func (s *Service) commitRecord(ctx context.Context, rec *artifacts.MediaRecord, requireDrive bool) (*artifacts.FinalizeResult, error) {
	if s.finalizer == nil {
		return nil, ErrFinalizerUnavailable
	}
	result, err := s.finalizer.Finalize(ctx, rec, artifacts.FinalizeOptions{RequireDrive: requireDrive, VerifyDB: true})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("finalizer returned nil result")
	}
	if !result.OK {
		message := result.Error
		if message == "" {
			message = "finalizer returned an unsuccessful result"
		}
		return result, errors.New(message)
	}
	return result, nil
}

// CheckDuplicate is the read-only preview of the ProcessAsset dedup decision.
// It reports the SAME four identity cases without publishing anything:
// "would_skip_duplicate" (same asset, same bytes), "would_reuse_storage"
// (different asset, same bytes) and "would_process" (everything else — which
// includes the same asset with CHANGED bytes, i.e. a replacement).
func (s *Service) CheckDuplicate(ctx context.Context, input *FinalizeInput, fileHash string) (*FinalizeResult, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: input is required", ErrFinalizationFailed)
	}
	out := &FinalizeResult{Status: "failed", LocalPath: input.LocalPath}
	contentHash, _, _ := byteIdentity(input.LocalPath)
	if contentHash == "" {
		contentHash = canonicalCallerHash(fileHash)
	}
	out.ContentHash = contentHash
	if s.dedupe != nil && s.dedupe.Policy().Enabled && s.store == nil {
		return out, ErrAssetStoreUnavailable
	}
	if s.dedupe == nil || !s.dedupe.Policy().Enabled {
		out.OK, out.Status = true, "no_dedupe_policy"
		return out, nil
	}
	match, err := s.dedupe.CheckDuplicate(ctx, assetop.ExistingAssetQuery{
		ID: input.ID, ContentSHA256: contentHash,
		DriveFileID: input.DriveFileID, Filename: input.Filename, Source: input.Source,
	})
	if err != nil {
		return out, fmt.Errorf("%w: duplicate check: %w", ErrFinalizationFailed, err)
	}
	switch {
	case match != nil && match.Kind == assetop.DuplicateSameAsset && match.SameContent && s.dedupe.Policy().SkipIfExists:
		existing := match.Record
		out.OK, out.Status = true, "would_skip_duplicate"
		out.DriveLink, out.DriveFileID, out.DownloadLink, out.LegacyFileMD5 = existing.DriveLink, existing.DriveFileID, existing.DownloadLink, existing.LegacyFileMD5
		return out, nil
	case match != nil && match.Kind == assetop.DuplicateSameContent && match.Record != nil:
		out.OK, out.Status = true, "would_reuse_storage"
		out.ReusedAssetID = match.Record.ID
		return out, nil
	}
	out.OK, out.Status = true, "would_process"
	return out, nil
}

func (s *Service) Reconcile(ctx context.Context, source string) (int, error) {
	if s.reconcile == nil {
		return 0, ErrReconcilerUnavailable
	}
	return s.reconcile.ReconcileDriveMissing(ctx, source)
}
func DefaultConfig() Config {
	return Config{DuplicatePolicy: assetop.DefaultDuplicatePolicy(), UploadPolicy: assetop.DefaultUploadPolicy(), PersistPolicy: assetop.DefaultPersistPolicy(), ReconcilePolicy: assetop.DefaultReconcilePolicy()}
}
