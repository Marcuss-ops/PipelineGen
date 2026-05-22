package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
	drivequery "velox/go-master/internal/storage/drive"
)

const folderMimeType = "application/vnd.google-apps.folder"

type Target struct {
	Name         string
	RootFolderID string
	Source       string
	MediaType    string
	Repo         *clips.Repository
}

type RootSummary struct {
	Name         string `json:"name"`
	RootFolderID string `json:"root_folder_id"`
	Source       string `json:"source"`
	MediaType    string `json:"media_type"`
	Requested    int    `json:"requested"`
	Synced       int    `json:"synced"`
	Failed       int    `json:"failed"`
	Error        string `json:"error,omitempty"`
}

type Summary struct {
	OK        bool          `json:"ok"`
	Roots     []RootSummary `json:"roots,omitempty"`
	Synced    int           `json:"synced"`
	Failed    int           `json:"failed"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Error     string        `json:"error,omitempty"`
}

type Service struct {
	driveClient *driveapi.Service
	log         *zap.Logger
	targets     []Target
	assetIndex  *assetindex.Service
	assetTree   *assettree.Service
	mu          sync.Mutex
}

func NewService(driveClient *driveapi.Service, targets []Target, assetIndex *assetindex.Service, assetTree *assettree.Service, log *zap.Logger) *Service {
	return &Service{
		driveClient: driveClient,
		log:         log,
		targets:     targets,
		assetIndex:  assetIndex,
		assetTree:   assetTree,
	}
}

func (s *Service) SyncAll(ctx context.Context) (*Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	summary := &Summary{
		OK:        true,
		StartedAt: time.Now().UTC(),
		Roots:     make([]RootSummary, 0, len(s.targets)),
	}

	if s.driveClient == nil {
		summary.OK = false
		summary.Error = "drive client not configured"
		return summary, fmt.Errorf("drive client not configured")
	}

	for _, target := range s.targets {
		if strings.TrimSpace(target.RootFolderID) == "" || target.Repo == nil {
			continue
		}

		rootSummary, err := s.syncTarget(ctx, target)
		if err != nil {
			rootSummary.Error = err.Error()
			summary.OK = false
			summary.Error = err.Error()
		}
		summary.Roots = append(summary.Roots, rootSummary)
		summary.Synced += rootSummary.Synced
		summary.Failed += rootSummary.Failed
	}

	summary.EndedAt = time.Now().UTC()
	return summary, nil
}

// SyncSource synchronizes a specific source target.
func (s *Service) SyncSource(ctx context.Context, source string) (*RootSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, target := range s.targets {
		if strings.EqualFold(target.Source, source) {
			summary, err := s.syncTarget(ctx, target)
			return &summary, err
		}
	}

	return nil, fmt.Errorf("source not found: %s", source)
}

func (s *Service) syncTarget(ctx context.Context, target Target) (RootSummary, error) {
	rootSummary := RootSummary{
		Name:         target.Name,
		RootFolderID: target.RootFolderID,
		Source:       target.Source,
		MediaType:    target.MediaType,
	}

	seenFolderIDs := make(map[string]struct{})
	markFolderSeen(seenFolderIDs, target.RootFolderID)

	rootMeta, err := s.driveClient.Files.Get(target.RootFolderID).Fields("id, name, webViewLink").Context(ctx).Do()
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
		rootLink = "https://driveapi.google.com/drive/folders/" + target.RootFolderID
	}

	now := time.Now().UTC()
	rootClip := &models.MediaAsset{
		ID:             target.RootFolderID,
		Name:           rootName,
		Filename:       rootName,
		FolderID:       target.RootFolderID,
		ParentFolderID: "", // Root has no parent
		Depth:          0,
		IsFolder:       true,
		FolderPath:     rootName,
		Group:          target.Source,
		MediaType:      target.MediaType,
		DriveLink:      rootLink,
		DownloadLink:   rootLink,
		Source:         target.Source,
		Category:       "folder",
		ExternalURL:    rootLink,
		Tags:           []string{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
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

func (s *Service) syncFolderRecursive(ctx context.Context, repo *clips.Repository, folderID, rootID, folderPath string, target Target, seenFolderIDs map[string]struct{}) (int, int, int, error) {
	children, err := s.listChildren(ctx, folderID)
	if err != nil {
		return 0, 0, 1, err
	}

	requested := len(children)
	synced := 0
	failed := 0

	for _, child := range children {
		if child == nil {
			continue
		}

		childName := strings.TrimSpace(child.Name)
		if childName == "" {
			childName = child.Id
		}

		childPath := path.Join(folderPath, childName)
		link := strings.TrimSpace(child.WebViewLink)
		if link == "" {
			link = strings.TrimSpace(child.WebContentLink)
		}
		if link == "" {
			if child.MimeType == folderMimeType {
				link = "https://drive.google.com/drive/folders/" + child.Id
			} else {
				link = "https://drive.google.com/file/d/" + child.Id
			}
		}

		category := "file"
		if child.MimeType == folderMimeType {
			category = "folder"
			markFolderSeen(seenFolderIDs, child.Id)
		}

		record := &models.MediaAsset{
			ID:             child.Id,
			Name:           childName,
			Filename:       childName,
			FolderID:       folderID,
			ParentFolderID: folderID,
			Depth:          strings.Count(childPath, "/"),
			IsFolder:       child.MimeType == folderMimeType,
			FolderPath:     childPath,
			Group:          target.Source,
			MediaType:      target.MediaType,
			DriveLink:      link,
			DownloadLink:   link,
			Source:         target.Source,
			Category:       category,
			ExternalURL:    link,
			Tags:           []string{},
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}

		if err := s.upsertPreservingExisting(ctx, repo, record); err != nil {
			failed++
			continue
		}

		// Save to common AssetTree
		if s.assetTree != nil {
			node := s.assetTree.NormalizeDriveNode(
				child.Id, childName, child.MimeType, child.WebViewLink, child.WebContentLink, folderID, rootID, folderPath, target.Source, child.Id,
			)
			if err := s.assetTree.UpsertNode(ctx, node); err != nil {
				s.log.Warn("failed to save node to asset tree", zap.Error(err), zap.String("id", child.Id))
			}
		}

		synced++

		if child.MimeType == folderMimeType {
			subRequested, subSynced, subFailed, err := s.syncFolderRecursive(ctx, repo, child.Id, rootID, childPath, target, seenFolderIDs)
			requested += subRequested
			synced += subSynced
			failed += subFailed
			if err != nil {
				s.log.Warn("recursive sync folder failed",
					zap.String("folder_id", child.Id),
					zap.String("path", childPath),
					zap.Error(err),
				)
			}
		}
	}

	return requested, synced, failed, nil
}

func (s *Service) pruneMissingFolders(ctx context.Context, repo *clips.Repository, source string, seenFolderIDs map[string]struct{}) error {
	if repo == nil {
		return nil
	}

	folders, err := repo.ListClipFolders(ctx, source)
	if err != nil {
		return err
	}

	for _, folder := range folders {
		if folder == nil {
			continue
		}
		if folder.FolderID == "" {
			continue
		}
		if _, ok := seenFolderIDs[folder.FolderID]; ok {
			continue
		}
		if err := repo.DeleteClipFolder(ctx, folder.ID); err != nil {
			return err
		}
		if s.assetTree != nil {
			if err := s.assetTree.DeleteNode(ctx, folder.FolderID); err != nil {
				s.log.Warn("failed to remove missing folder from asset tree",
					zap.String("folder_id", folder.FolderID),
					zap.Error(err),
				)
			}
		}
	}

	return nil
}

func markFolderSeen(seen map[string]struct{}, folderID string) {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" || seen == nil {
		return
	}
	seen[folderID] = struct{}{}
}

func (s *Service) listChildren(ctx context.Context, folderID string) ([]*driveapi.File, error) {
	query := drivequery.BuildQuery(folderID)
	call := s.driveClient.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink)").
		PageSize(1000).
		Context(ctx)

	var files []*driveapi.File
	err := call.Pages(ctx, func(fl *driveapi.FileList) error {
		files = append(files, fl.Files...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Service) upsertPreservingExisting(ctx context.Context, repo *clips.Repository, clip *models.MediaAsset) error {
	if repo == nil || clip == nil {
		return nil
	}

	if existing, err := repo.GetClip(ctx, clip.ID); err == nil && existing != nil {
		if existing.FileHash != "" {
			clip.FileHash = existing.FileHash
		}
		if existing.LocalPath != "" {
			clip.LocalPath = existing.LocalPath
		}
		if len(existing.Metadata) > 0 {
			clip.Metadata = existing.Metadata
		}
		if !existing.CreatedAt.IsZero() {
			clip.CreatedAt = existing.CreatedAt
		}
		clip.Tags = mergeTags(clip.Tags, existing.Tags)
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		return err
	}

	// Write to asset_index for unified tracking
	if s.assetIndex != nil {
		assetRec := &assetindex.AssetRecord{
			AssetID:   clip.Source + "_" + clip.ID,
			AssetType: clip.MediaType,
			Source:    clip.Source,
			SourceID:  clip.ID,
			GroupName: clip.Group,
			LocalPath: clip.LocalPath,
			DriveLink: clip.DriveLink,
			FileHash:  clip.FileHash,
			Status:    "ready",
			Metadata:  clip.MetadataJSON(),
			CreatedAt: clip.CreatedAt,
			UpdatedAt: clip.UpdatedAt,
		}
		if err := s.assetIndex.Upsert(ctx, assetRec); err != nil {
			s.log.Warn("failed to write stock clip to asset_index", zap.Error(err))
		}
	}

	return nil
}

func mergeTags(base, extra []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(base)+len(extra))
	add := func(items []string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	add(base)
	add(extra)
	return out
}

// HandleJob processes a catalog.sync job.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling catalog.sync job", zap.String("job_id", job.ID))

	var payload struct {
		Source string `json:"source"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting catalog synchronization")
	}

	var result map[string]any
	if payload.Source != "" {
		s.log.Info("syncing specific source", zap.String("source", payload.Source))
		summary, err := s.SyncSource(ctx, payload.Source)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":        true,
			"source":    payload.Source,
			"requested": summary.Requested,
			"synced":    summary.Synced,
			"failed":    summary.Failed,
		}
	} else {
		s.log.Info("syncing all sources")
		summary, err := s.SyncAll(ctx)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"ok":     summary.OK,
			"synced": summary.Synced,
			"failed": summary.Failed,
			"roots":  summary.Roots,
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog synchronization completed")
	}

	return result, nil
}

// RegisterHandler registers this service as a handler for catalog.sync jobs.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeCatalogSync, s.HandleJob)
		s.log.Info("registered catalog.sync job handler")
	}
}
