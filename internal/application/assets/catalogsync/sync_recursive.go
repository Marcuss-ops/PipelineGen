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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	storedrive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

const folderMimeType = "application/vnd.google-apps.folder"

func (s *Service) syncTarget(ctx context.Context, target Target) (RootSummary, error) {
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
		MediaType: asset.MediaType(target.MediaType),
		Source:    asset.Source(target.Source),
		Category:  "folder",
		Tags:      []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	rootClip.SetFolderID(target.RootFolderID)
	rootClip.SetParentFolderID("")
	rootClip.SetDepth(0)
	rootClip.SetIsFolder(true)
	rootClip.SetFolderPath(rootName)
	rootClip.SetDriveLink(rootLink)
	rootClip.SetDownloadLink(rootLink)
	rootClip.SetExternalURL(rootLink)
	rootClip.SetDriveFileID(target.RootFolderID)
	if err := s.upsertPreservingExisting(ctx, target.Repo, rootClip); err != nil {
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

	requested, synced, failed, err := s.syncFolderRecursive(ctx, target.Repo, target.RootFolderID, target.RootFolderID, rootName, target, seenFolderIDs)
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

func (s *Service) syncFolderRecursive(ctx context.Context, repo *assets.ClipsRepository, folderID, rootID, folderPath string, target Target, seenFolderIDs map[string]struct{}) (int, int, int, error) {
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
				link = storedrive.FolderURLFromID(child.ID)
			} else {
				link = storedrive.FileURLFromID(child.ID)
			}
		}

		category := "file"
		if child.MimeType == folderMimeType {
			category = "folder"
			markFolderSeen(seenFolderIDs, child.ID)
		}

		now := time.Now().UTC()
		clip := &asset.Asset{
			ID:        child.ID,
			Name:      childName,
			Filename:  childName,
			Group:     target.Source,
			MediaType: asset.MediaType(target.MediaType),
			Source:    asset.Source(target.Source),
			Category:  category,
			Tags:      []string{},
			CreatedAt: now,
			UpdatedAt: now,
		}
		clip.SetFolderID(child.ID)
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

		if err := s.upsertPreservingExisting(ctx, repo, clip); err != nil {
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
			subRequested, subSynced, subFailed, err := s.syncFolderRecursive(ctx, repo, child.ID, rootID, childPath, target, seenFolderIDs)
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
func remoteFileFingerprint(file drive.DriveFileInfo) string {
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
func (s *Service) listChildren(ctx context.Context, folderID string) ([]drive.DriveFileInfo, error) {
	return s.reader.ListFiles(ctx, folderID)
}
