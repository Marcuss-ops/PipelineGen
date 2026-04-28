// Package interfaces defines the contracts between layers.
// Services should depend on these interfaces, not concrete implementations.
package interfaces

import (
	"context"

	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/ml/ollama/types"
)

// ============================================================================
// Script Generation
// ============================================================================

// ScriptGenerator generates text scripts via AI.
type ScriptGenerator interface {
	// GenerateFromText generates a script from source text and request params
	GenerateFromText(ctx context.Context, req *types.TextGenerationRequest) (*types.GenerationResult, error)
	// GetClient returns the underlying Ollama client for direct access
	GetClient() *client.Client
}

// ============================================================================
// Entity Extraction
// ============================================================================

// EntityExtractor extracts structured entities from raw text.
type EntityExtractor interface {
	// ExtractEntities returns sentences, proper nouns, and keywords from text
	ExtractEntities(text string) (sentences, properNouns, keywords []string)
}

// ============================================================================
// Stock Management
// ============================================================================

// StockFolder represents a stock folder on Drive.
type StockFolder struct {
	DriveID  string
	ParentID string
	FullPath string
	Section  string
	TopicSlug string
}

// StockClipEntry represents a stock clip entry.
type StockClipEntry struct {
	ClipID   string
	FolderID string
	Filename string
	Source   string
	Tags     []string
	Duration int
}

// StockStore provides access to stock folders and clips.
type StockStore interface {
	// FindFolderByTopic finds a folder matching the topic (partial match)
	FindFolderByTopic(topic string) (*StockFolder, error)
	// SearchClipsByTags returns clips matching any of the given tags
	SearchClipsByTags(tags []string) ([]StockClipEntry, error)
	// UpsertFolder creates or updates a folder entry
	UpsertFolder(StockFolderEntry) error
	// BulkUpsertFolders creates or updates multiple folder entries
	BulkUpsertFolders([]StockFolderEntry) error
	// UpsertClip creates or updates a clip entry
	UpsertClip(StockClipEntry) error
	// BulkUpsertClips creates or updates multiple clip entries
	BulkUpsertClips([]StockClipEntry) error
}

// StockFolderEntry is the input for creating/updating a folder.
type StockFolderEntry struct {
	TopicSlug string
	DriveID   string
	ParentID  string
	FullPath  string
	Section   string
}

// ============================================================================
// Clip Cache
// ============================================================================

// ClipCacher caches downloaded clips to avoid duplicate downloads.
type ClipCacher interface {
	// Search returns a cached clip matching the query, or nil if not found
	Search(query string) *CachedClip
	// Store saves a clip to the cache
	Store(clip *CachedClip)
	// Cleanup removes expired entries and returns the count of removed items
	Cleanup() int
}

// CachedClip represents a cached downloaded clip.
type CachedClip struct {
	SearchQuery  string
	VideoID      string
	Title        string
	URL          string
	DriveURL     string
	DriveFileID  string
	Duration     int
	ViewCount    int
	DownloadedAt int64
	LastUsedAt   int64
	UseCount     int
}

// ============================================================================
// Document Management
// ============================================================================

// DocService creates and manages Google Docs.
type DocService interface {
	// CreateDoc creates a new Google Doc with the given title and content
	CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error)
	// AppendToDoc appends content to an existing doc
	AppendToDoc(ctx context.Context, docID, content string) error
	// AppendToDocByURL appends content to a doc identified by its URL
	AppendToDocByURL(ctx context.Context, docURL, content string) error
	// GetDocContent returns the content of a doc
	GetDocContent(ctx context.Context, docID string) (string, error)
}

// Doc represents a created Google Doc.
type Doc struct {
	ID    string
	Title string
	URL   string
}

// ============================================================================
// Drive Operations
// ============================================================================

// DriveUploader uploads files and manages folders on Google Drive.
type DriveUploader interface {
	// UploadFile uploads a file to the specified folder
	UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error)
	// CreateFolder creates a folder on Drive
	CreateFolder(ctx context.Context, name, parentID string) (string, error)
	// GetOrCreateFolder gets an existing folder or creates it
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
}
