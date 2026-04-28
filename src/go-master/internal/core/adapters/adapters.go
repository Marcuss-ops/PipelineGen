// Package adapters provides adapter implementations for core interfaces.
package adapters

import (
	"context"

	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/core/interfaces"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
)

// ============================================================================
// ScriptGenerator Adapter
// ============================================================================

// ScriptGeneratorAdapter wraps ollama.Generator to satisfy ScriptGenerator interface.
type ScriptGeneratorAdapter struct {
	*ollama.Generator
}

// GetClient returns the underlying Ollama client.
func (a *ScriptGeneratorAdapter) GetClient() *client.Client {
	return a.Generator.GetClient()
}

// ============================================================================
// StockStore Adapter
// ============================================================================

// StockStoreAdapter wraps stockdb.StockDB to satisfy StockStore interface.
type StockStoreAdapter struct {
	*stockdb.StockDB
}

// FindFolderByTopic delegates to the underlying DB.
func (a *StockStoreAdapter) FindFolderByTopic(topic string) (*interfaces.StockFolder, error) {
	folder, err := a.StockDB.FindFolderByTopic(topic)
	if err != nil || folder == nil {
		return nil, err
	}
	return &interfaces.StockFolder{
		DriveID:   folder.DriveID,
		ParentID:  folder.ParentID,
		FullPath:  folder.FullPath,
		Section:   folder.Section,
		TopicSlug: folder.TopicSlug,
	}, nil
}

// SearchClipsByTags delegates to the underlying DB.
func (a *StockStoreAdapter) SearchClipsByTags(tags []string) ([]interfaces.StockClipEntry, error) {
	clips, err := a.StockDB.SearchClipsByTags(tags)
	if err != nil {
		return nil, err
	}
	result := make([]interfaces.StockClipEntry, len(clips))
	for i, c := range clips {
		result[i] = interfaces.StockClipEntry{
			ClipID:   c.ClipID,
			FolderID: c.FolderID,
			Filename: c.Filename,
			Source:   c.Source,
			Tags:     c.Tags,
			Duration: c.Duration,
		}
	}
	return result, nil
}

// UpsertFolder delegates to the underlying DB.
func (a *StockStoreAdapter) UpsertFolder(entry interfaces.StockFolderEntry) error {
	return a.StockDB.UpsertFolder(stockdb.StockFolderEntry{
		TopicSlug: entry.TopicSlug,
		DriveID:   entry.DriveID,
		ParentID:  entry.ParentID,
		FullPath:  entry.FullPath,
		Section:   entry.Section,
	})
}

// BulkUpsertFolders delegates to the underlying DB.
func (a *StockStoreAdapter) BulkUpsertFolders(entries []interfaces.StockFolderEntry) error {
	dbEntries := make([]stockdb.StockFolderEntry, len(entries))
	for i, e := range entries {
		dbEntries[i] = stockdb.StockFolderEntry{
			TopicSlug: e.TopicSlug,
			DriveID:   e.DriveID,
			ParentID:  e.ParentID,
			FullPath:  e.FullPath,
			Section:   e.Section,
		}
	}
	return a.StockDB.BulkUpsertFolders(dbEntries)
}

// UpsertClip delegates to the underlying DB.
func (a *StockStoreAdapter) UpsertClip(entry interfaces.StockClipEntry) error {
	return a.StockDB.UpsertClip(stockdb.StockClipEntry{
		ClipID:   entry.ClipID,
		FolderID: entry.FolderID,
		Filename: entry.Filename,
		Source:   entry.Source,
		Tags:     entry.Tags,
		Duration: entry.Duration,
	})
}

// BulkUpsertClips delegates to the underlying DB.
func (a *StockStoreAdapter) BulkUpsertClips(entries []interfaces.StockClipEntry) error {
	dbEntries := make([]stockdb.StockClipEntry, len(entries))
	for i, e := range entries {
		dbEntries[i] = stockdb.StockClipEntry{
			ClipID:   e.ClipID,
			FolderID: e.FolderID,
			Filename: e.Filename,
			Source:   e.Source,
			Tags:     e.Tags,
			Duration: e.Duration,
		}
	}
	return a.StockDB.BulkUpsertClips(dbEntries)
}

// ============================================================================
// ClipCacher Adapter
// ============================================================================

// ClipCacherAdapter wraps clipcache.ClipCache to satisfy ClipCacher interface.
type ClipCacherAdapter struct {
	*clipcache.ClipCache
}

// Search delegates to the underlying cache.
func (a *ClipCacherAdapter) Search(query string) *interfaces.CachedClip {
	clip := a.ClipCache.Search(query)
	if clip == nil {
		return nil
	}
	return &interfaces.CachedClip{
		SearchQuery:  clip.SearchQuery,
		VideoID:      clip.VideoID,
		Title:        clip.Title,
		URL:          clip.URL,
		DriveURL:     clip.DriveURL,
		DriveFileID:  clip.DriveFileID,
		Duration:     clip.Duration,
		ViewCount:    clip.ViewCount,
		DownloadedAt: clip.DownloadedAt,
		LastUsedAt:   clip.LastUsedAt,
		UseCount:     clip.UseCount,
	}
}

// Store delegates to the underlying cache.
func (a *ClipCacherAdapter) Store(clip *interfaces.CachedClip) {
	a.ClipCache.Store(&clipcache.CachedClip{
		SearchQuery:  clip.SearchQuery,
		VideoID:      clip.VideoID,
		Title:        clip.Title,
		URL:          clip.URL,
		DriveURL:     clip.DriveURL,
		DriveFileID:  clip.DriveFileID,
		Duration:     clip.Duration,
		ViewCount:    clip.ViewCount,
		DownloadedAt: clip.DownloadedAt,
		LastUsedAt:   clip.LastUsedAt,
		UseCount:     clip.UseCount,
	})
}

// Cleanup delegates to the underlying cache.
func (a *ClipCacherAdapter) Cleanup() int {
	return a.ClipCache.Cleanup()
}

// ============================================================================
// DocService Adapter
// ============================================================================

// DocServiceAdapter wraps drive.DocClient to satisfy DocService interface.
type DocServiceAdapter struct {
	*drive.DocClient
}

// CreateDoc delegates to the underlying DocClient.
func (a *DocServiceAdapter) CreateDoc(ctx context.Context, title, content, folderID string) (*interfaces.Doc, error) {
	doc, err := a.DocClient.CreateDoc(ctx, title, content, folderID)
	if err != nil {
		return nil, err
	}
	return &interfaces.Doc{
		ID:    doc.ID,
		Title: doc.Title,
		URL:   doc.URL,
	}, nil
}

// AppendToDoc delegates to the underlying DocClient.
func (a *DocServiceAdapter) AppendToDoc(ctx context.Context, docID, content string) error {
	return a.DocClient.AppendToDoc(ctx, docID, content)
}

// AppendToDocByURL delegates to the underlying DocClient.
func (a *DocServiceAdapter) AppendToDocByURL(ctx context.Context, docURL, content string) error {
	return a.DocClient.AppendToDocByURL(ctx, docURL, content)
}

// GetDocContent delegates to the underlying DocClient.
func (a *DocServiceAdapter) GetDocContent(ctx context.Context, docID string) (string, error) {
	return a.DocClient.GetDocContent(ctx, docID)
}

// ============================================================================
// DriveUploader Adapter
// ============================================================================

// DriveUploaderAdapter wraps drive.DocClient to satisfy DriveUploader interface.
type DriveUploaderAdapter struct {
	*drive.DocClient
}

// UploadFile delegates to the underlying Drive client.
func (a *DriveUploaderAdapter) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	result, err := a.DocClient.UploadFile(ctx, filename, filePath, "", folderID)
	if err != nil {
		return "", err
	}
	return result.Id, nil
}

// CreateFolder delegates to the underlying Drive client.
func (a *DriveUploaderAdapter) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return a.DocClient.GetOrCreateFolder(ctx, name, parentID)
}
