package catalogsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

const folderMimeType = "application/vnd.google-apps.folder"

func validateTarget(target Target) error {
	if strings.TrimSpace(target.RootFolderID) == "" {
		return fmt.Errorf("%w: source=%q has no root folder", ErrCatalogSyncInvalidTarget, target.Source)
	}
	if target.Repo == nil || target.Indexer == nil {
		return fmt.Errorf("%w: source=%q", ErrCatalogSyncInvalidTarget, target.Source)
	}
	return nil
}

func (s *Service) syncTarget(ctx context.Context, target Target) (RootSummary, error) {
	if err := validateTarget(target); err != nil {
		return RootSummary{
			Name:         target.Name,
			RootFolderID: target.RootFolderID,
			Source:       target.Source,
			MediaType:    target.MediaType,
			Failed:       1,
			Error:        err.Error(),
		}, err
	}

	rootSummary := RootSummary{
		Name:         target.Name,
		RootFolderID: target.RootFolderID,
		Source:       target.Source,
		MediaType:    target.MediaType,
	}

	seenFolderIDs := make(map[string]struct{})
	markFolderSeen(seenFolderIDs, target.RootFolderID)

	rootMeta, err := s.reader.GetFileMeta(ctx, target.RootFolderID)
	if err != nil {
		rootSummary.Failed++
		return rootSummary, err
	}

	rootName := strings.TrimSpace(target.Name)
	if rootName == "" && rootMeta != nil {
		rootName = strings.TrimSpace(rootMeta.Name)
	}
	if rootName == "" {
		rootName = target.RootFolderID
	}

	rootLink := ""
	if rootMeta != nil {
		rootLink = strings.TrimSpace(rootMeta.WebViewLink)
	}
	if rootLink == "" {
		rootLink = "https://drive.google.com/drive/folders/" + target.RootFolderID
	}

	now := time.Now().UTC()
	rootClip := &asset.Asset{
		ID:        target.RootFolderID,
		Name:      rootName,
		Filename:  rootName,
		Group:     target.Source,
		MediaType: asset.MediaType("folder"),
		Source:    asset.Source(target.Source),
		Category:  "folder",
		Tags:      []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Folder metadata is persisted in media_assets as a non-vector asset.
	// It still needs a valid lifecycle state; leaving this empty makes the
	// canonical SQLite state trigger reject the whole recursive sync.
	rootClip.LifecycleState = asset.StateActive
	rootClip.SetFolderID(target.RootFolderID)
	rootClip.SetParentFolderID("")
	rootClip.SetDepth(0)
	rootClip.SetIsFolder(true)
	rootClip.SetFolderPath(rootName)
	rootClip.SetDriveLink(rootLink)
	rootClip.SetDownloadLink(rootLink)
	rootClip.SetExternalURL(rootLink)
	rootClip.SetDriveFileID(target.RootFolderID)
	if err := s.upsertPreservingExisting(ctx, target.Repo, target.Indexer, rootClip); err != nil {
		rootSummary.Failed++
		return rootSummary, err
	}

	// Save to common AssetTree
	if s.assetTree != nil {
		node := s.assetTree.NormalizeDriveNode(
			target.RootFolderID, rootName, folderMimeType, rootLink, rootLink, "", target.RootFolderID, "", target.Source, target.RootFolderID,
		)
		if err := s.assetTree.UpsertNode(ctx, node); err != nil {
			s.log.Warn("failed to save root to asset tree", zap.Error(err), zap.String("id", target.RootFolderID))
		}
	}

	rootSummary.Synced++

	requested, synced, failed, err := s.syncFolderRecursive(ctx, target.Repo, target.Indexer, target.RootFolderID, target.RootFolderID, rootName, target, seenFolderIDs)
	rootSummary.Requested = requested
	rootSummary.Synced += synced
	rootSummary.Failed += failed

	if err == nil {
		if pruneErr := s.pruneMissingFolders(ctx, target.Repo, target.Source, seenFolderIDs); pruneErr != nil {
			rootSummary.Failed++
			err = pruneErr
		}
	} else {
		s.log.Warn("skipping folder prune because sync failed",
			zap.String("source", target.Source),
			zap.Error(err),
		)
	}

	return rootSummary, err
}

func (s *Service) syncFolderRecursive(ctx context.Context, repo CatalogRepository, indexer AssetIndexer, folderID, rootID, folderPath string, target Target, seenFolderIDs map[string]struct{}) (int, int, int, error) {
	children, err := s.listChildren(ctx, folderID)
	if err != nil {
		return 0, 0, 1, err
	}

	requested := len(children)
	synced := 0
	failed := 0

	for _, child := range children {
		childName := strings.TrimSpace(child.Name)
		if childName == "" {
			childName = child.ID
		}

		childPath := path.Join(folderPath, childName)
		link := strings.TrimSpace(child.WebViewLink)
		if link == "" {
			link = strings.TrimSpace(child.WebContentLink)
		}
		if link == "" {
			if child.MimeType == folderMimeType {
				link = "https://drive.google.com/drive/folders/" + child.ID
			} else {
				link = "https://drive.google.com/file/d/" + child.ID
			}
		}

		category := "file"
		if child.MimeType == folderMimeType {
			category = "folder"
			markFolderSeen(seenFolderIDs, child.ID)
		}

		now := time.Now().UTC()
		childMediaType := target.MediaType
		if child.MimeType == folderMimeType {
			// A Drive folder is catalog metadata, never a media clip. Keeping
			// it as media_type=clip makes enrichment attempt downloads of
			// Google Workspace folders and pollutes the clip candidate set.
			childMediaType = "folder"
		}
		clip := &asset.Asset{
			ID:        child.ID,
			Name:      childName,
			Filename:  childName,
			Group:     target.Source,
			MediaType: asset.MediaType(childMediaType),
			Source:    asset.Source(target.Source),
			Category:  category,
			Tags:      []string{},
			CreatedAt: now,
			UpdatedAt: now,
		}
		// A Drive catalog sync discovers already-published remote files. The
		// file is therefore online immediately; indexing is tracked separately
		// by index_state and the transactional outbox. Folders remain metadata
		// nodes and are never made indexable below.
		// Both files and folder metadata are live Drive discoveries. Folders
		// remain non-indexable via IsFolder, but must still carry a valid
		// lifecycle state for the SQLite state trigger.
		clip.LifecycleState = asset.StateActive
		// For a file, folder_id is the containing Drive folder. A file ID is
		// not a valid Drive parent and must never be used as the destination
		// for sidecar artifacts. Folder nodes keep their own ID so recursive
		// traversal and reconciliation can address them directly.
		if child.MimeType == folderMimeType {
			clip.SetFolderID(child.ID)
		} else {
			clip.SetFolderID(folderID)
		}
		clip.SetParentFolderID(folderID)
		clip.SetDepth(0)
		clip.SetIsFolder(child.MimeType == folderMimeType)
		clip.SetFolderPath(childPath)
		clip.SetDriveLink(link)
		clip.SetDownloadLink(link)
		clip.SetExternalURL(link)
		// Keep the canonical Drive identity separate from the generated
		// web/download links. Resolution, deduplication and future Drive
		// reconciliation use this stable file ID.
		clip.SetDriveFileID(child.ID)
		clip.SetMetadataString("mime_type", child.MimeType)
		if child.MimeType != folderMimeType {
			clip.SetFileHash(remoteFileFingerprint(child))
		}

		if err := s.upsertPreservingExisting(ctx, repo, indexer, clip); err != nil {
			s.log.Warn("failed to upsert clip", zap.String("id", child.ID), zap.Error(err))
			failed++
			continue
		}

		// Save to common AssetTree
		if s.assetTree != nil {
			node := s.assetTree.NormalizeDriveNode(
				child.ID, childName, child.MimeType, link, link, folderID, rootID, folderPath, target.Source, child.ID,
			)
			if err := s.assetTree.UpsertNode(ctx, node); err != nil {
				s.log.Warn("failed to save node to asset tree", zap.Error(err), zap.String("id", child.ID))
			}
		}

		synced++

		if child.MimeType == folderMimeType {
			subRequested, subSynced, subFailed, err := s.syncFolderRecursive(ctx, repo, indexer, child.ID, rootID, childPath, target, seenFolderIDs)
			requested += subRequested
			synced += subSynced
			failed += subFailed
			if err != nil {
				s.log.Warn("recursive sync folder failed",
					zap.String("folder_id", child.ID),
					zap.String("path", childPath),
					zap.Error(err),
				)
			}
		}
	}

	return requested, synced, failed, nil
}

// remoteFileFingerprint supplies the stable source fingerprint required by
// the canonical supersede gate before a Drive asset is downloaded locally.
// Google Drive's MD5 is the strongest available value; the metadata fallback
// remains deterministic across retries and is explicitly not presented as a
// content hash.
func remoteFileFingerprint(file RemoteFile) string {
	if strings.TrimSpace(file.MD5Checksum) != "" {
		return "drive-md5:" + strings.TrimSpace(file.MD5Checksum)
	}
	payload := file.ID + "\x00" + file.Name + "\x00" + file.MimeType + "\x00" + fmt.Sprintf("%d", file.Size)
	sum := sha256.Sum256([]byte(payload))
	return "drive-meta-sha256:" + hex.EncodeToString(sum[:])
}

// listChildren lists the direct children of a Drive folder. Kept in this file
// so the recursive walker can swap it in tests without touching higher-level
// sync orchestration.
func (s *Service) listChildren(ctx context.Context, folderID string) ([]RemoteFile, error) {
	return s.reader.ListFiles(ctx, folderID)
}
