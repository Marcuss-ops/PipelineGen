package clip

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
)

func (idx *Indexer) ScanAndIndex(ctx context.Context) error {
	startTime := time.Now()
	logger.Info("Starting clip index scan", zap.String("root_folder", idx.rootFolderID))

	if idx.driveClient == nil {
		logger.Warn("Skipping clip index scan because Drive client is nil")
		return nil
	}

	newIndex := &ClipIndex{
		Version:      "1.0",
		RootFolderID: idx.rootFolderID,
		LastSync:     time.Now(),
		Clips:        []IndexedClip{},
		Folders:      []IndexedFolder{},
		Stats: IndexStats{
			ClipsByGroup:     make(map[string]int),
			ClipsByMediaType: make(map[string]int),
		},
	}

	scanRoots := idx.getScanFolderIDs()
	if len(scanRoots) == 0 {
		err := idx.scanFolders(ctx, idx.rootFolderID, "", 0, 2, newIndex)
		if err != nil {
			return fmt.Errorf("failed to scan folders: %w", err)
		}
	} else {
		for _, rootID := range scanRoots {
			if err := idx.scanFolders(ctx, rootID, "", 0, 2, newIndex); err != nil {
				logger.Warn("Failed to scan configured clip root", zap.String("root_id", rootID), zap.Error(err))
			}
		}
	}

	newIndex.Stats.TotalClips = len(newIndex.Clips)
	newIndex.Stats.TotalFolders = len(newIndex.Folders)
	newIndex.Stats.LastScanDuration = time.Since(startTime)

	for _, clip := range newIndex.Clips {
		if clip.Group != "" {
			newIndex.Stats.ClipsByGroup[clip.Group]++
		}
		if clip.MediaType != "" {
			newIndex.Stats.ClipsByMediaType[clip.MediaType]++
		}
	}

	idx.mu.Lock()
	// Clear old index reference to help GC
	idx.index = nil  // Clear reference before reassignment
	idx.mu.Unlock()

	// Force GC to collect old index if no other references exist
	runtime.GC()

	idx.mu.Lock()
	idx.index = newIndex
	idx.lastSync = time.Now()
	idx.mu.Unlock()

	// Log memory stats periodically
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Info("Clip index scan completed",
		zap.Int("total_clips", newIndex.Stats.TotalClips),
		zap.Int("total_folders", newIndex.Stats.TotalFolders),
		zap.Duration("duration", time.Since(startTime)),
		zap.Uint64("heap_inuse_mb", m.HeapInuse/1024/1024),
		zap.Uint64("heap_objects", m.HeapObjects),
	)

	return nil
}

func (idx *Indexer) scanFolders(ctx context.Context, parentID, parentPath string, depth, maxDepth int, index *ClipIndex) error {
	if depth > maxDepth {
		return nil
	}

	opts := drive.ListFoldersOptions{
		ParentID: parentID,
		MaxDepth: 0,
		MaxItems: 50,
	}

	folders, err := idx.driveClient.ListFoldersNoRecursion(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to list folders at %s: %w", parentID, err)
	}

	for _, folder := range folders {
		folderPath := folder.Name
		if parentPath != "" {
			folderPath = parentPath + "/" + folder.Name
		}

		group := idx.detectGroupFromPath(folderPath)

		indexedFolder := IndexedFolder{
			ID:         folder.ID,
			Name:       folder.Name,
			Path:       folderPath,
			ParentID:   parentID,
			Group:      group,
			ModifiedAt: time.Now(),
			IndexedAt:  time.Now(),
		}
		index.Folders = append(index.Folders, indexedFolder)

		err := idx.scanFolderClips(ctx, folder.ID, folderPath, group, index)
		if err != nil {
			logger.Warn("Failed to scan folder clips",
				zap.String("folder", folder.Name),
				zap.Error(err))
		}

		err = idx.scanFolders(ctx, folder.ID, folderPath, depth+1, maxDepth, index)
		if err != nil {
			logger.Warn("Failed to scan subfolders",
				zap.String("folder", folder.Name),
				zap.Error(err))
		}
	}

	return nil
}

func (idx *Indexer) scanFolderClips(ctx context.Context, folderID, folderPath, group string, index *ClipIndex) error {
	content, err := idx.driveClient.GetFolderContent(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder content: %w", err)
	}

	mediaType := idx.detectMediaTypeFromPath(folderPath)

	for _, file := range content.Files {
		if !IsVideoFile(file.MimeType, file.Name) {
			continue
		}

		clipName := CleanClipName(file.Name)
		tags := idx.extractTags(clipName, folderPath, group)

		resolution := "unknown"
		var durationMs int64
		if file.Width > 0 && file.Height > 0 {
			resolution = fmt.Sprintf("%dx%d", file.Width, file.Height)
		} else {
			resolution = idx.detectResolutionFromName(file.Name)
		}

		if file.DurationMs > 0 {
			durationMs = file.DurationMs
		}

		downloadLink := fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", file.ID)

		indexedClip := IndexedClip{
			ID:           file.ID,
			Name:         clipName,
			Filename:     file.Name,
			FolderID:     folderID,
			FolderPath:   folderPath,
			Group:        group,
			MediaType:    mediaType,
			DriveLink:    file.Link,
			DownloadLink: downloadLink,
			Resolution:   resolution,
			Duration:     float64(durationMs),
			Width:        int(file.Width),
			Height:       int(file.Height),
			Size:         file.Size,
			MimeType:     file.MimeType,
			Tags:         tags,
			ModifiedAt:   file.ModifiedTime,
			IndexedAt:    time.Now(),
		}

		index.Clips = append(index.Clips, indexedClip)
	}

	return nil
}

func (idx *Indexer) IncrementalScan(ctx context.Context) (int, int, error) {
	idx.mu.RLock()
	lastSync := idx.lastSync
	oldIndex := idx.index
	idx.mu.RUnlock()

	// Clear cache to free memory
	idx.cache.Clear()

	if idx.driveClient == nil {
		return 0, 0, fmt.Errorf("drive client is nil")
	}

	if oldIndex == nil || lastSync.IsZero() || len(idx.getScanFolderIDs()) > 0 {
		return 0, 0, idx.ScanAndIndex(ctx)
	}

	existingClips := make(map[string]int)
	for i, c := range oldIndex.Clips {
		existingClips[c.ID] = i
	}

	existingFolders := make(map[string]*IndexedFolder)
	for i := range oldIndex.Folders {
		existingFolders[oldIndex.Folders[i].ID] = &oldIndex.Folders[i]
	}

	newClips := 0
	updatedFolders := 0

	rootOpts := drive.ListFoldersOptions{
		ParentID: idx.rootFolderID,
		MaxDepth: 0,
		MaxItems: 100,
	}

	rootFolders, err := idx.driveClient.ListFoldersNoRecursion(ctx, rootOpts)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list root folders: %w", err)
	}

	for _, folder := range rootFolders {
		folderModified, err := idx.getFolderModifiedTime(ctx, folder.ID)
		if err != nil {
			continue
		}

		if !folderModified.After(lastSync) {
			continue
		}

		folderPath := folder.Name
		group := idx.detectGroupFromPath(folderPath)

		oldCount := len(oldIndex.Clips)
		var remainingClips []IndexedClip
		for _, c := range oldIndex.Clips {
			if c.FolderID != folder.ID {
				remainingClips = append(remainingClips, c)
			}
		}
		oldIndex.Clips = remainingClips
		clipsRemoved := oldCount - len(remainingClips)

		err = idx.scanFolderClips(ctx, folder.ID, folderPath, group, oldIndex)
		if err != nil {
			continue
		}

		clipsAdded := len(oldIndex.Clips) - len(remainingClips)
		newClips += clipsAdded
		updatedFolders++

		logger.Info("Incremental scan updated folder",
			zap.String("folder", folder.Name),
			zap.Int("clips_removed", clipsRemoved),
			zap.Int("clips_added", clipsAdded))
	}

	idx.rebuildStats(oldIndex)

	idx.mu.Lock()
	// Clear reference to help GC
	idx.index = nil
	idx.mu.Unlock()

	// Force GC
	runtime.GC()

	idx.mu.Lock()
	idx.index = oldIndex
	idx.lastSync = time.Now()
	idx.mu.Unlock()

	// Log memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Info("Incremental scan completed",
		zap.Int("folders_updated", updatedFolders),
		zap.Int("clips_net_change", newClips),
		zap.Uint64("heap_inuse_mb", m.HeapInuse/1024/1024),
		zap.Int("folders_updated", updatedFolders),
		zap.Int("clips_net_change", newClips))

	return updatedFolders, newClips, nil
}

func (idx *Indexer) getFolderModifiedTime(ctx context.Context, folderID string) (time.Time, error) {
	opts := drive.ListFoldersOptions{
		ParentID: folderID,
		MaxDepth: 0,
		MaxItems: 1,
	}

	folders, err := idx.driveClient.ListFoldersNoRecursion(ctx, opts)
	if err != nil {
		return time.Time{}, err
	}

	if len(folders) == 0 {
		return time.Now(), nil
	}

	content, err := idx.driveClient.GetFolderContent(ctx, folderID)
	if err != nil {
		return time.Now(), nil
	}

	latestTime := time.Time{}
	for _, f := range content.Files {
		if f.ModifiedTime.After(latestTime) {
			latestTime = f.ModifiedTime
		}
	}

	return latestTime, nil
}

func (idx *Indexer) rebuildStats(index *ClipIndex) {
	index.Stats = IndexStats{
		ClipsByGroup:     make(map[string]int),
		ClipsByMediaType: make(map[string]int),
	}

	for _, clip := range index.Clips {
		if clip.Group != "" {
			index.Stats.ClipsByGroup[clip.Group]++
		}
		if clip.MediaType != "" {
			index.Stats.ClipsByMediaType[clip.MediaType]++
		}
	}

	index.Stats.TotalClips = len(index.Clips)
	index.Stats.TotalFolders = len(index.Folders)
}
