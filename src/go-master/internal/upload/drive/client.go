// Package drive provides Google Drive API integration for Agent 5.
package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Client gestisce le operazioni Google Drive
type Client struct {
	service     *drive.Service
	tokenSource oauth2.TokenSource
	tokenFile   string // Path to token file for refresh support
	credsData   []byte // OAuth credentials JSON
	scopes      []string
	mu          sync.RWMutex
}

// Config configurazione Drive
type Config struct {
	CredentialsFile string
	TokenFile       string
	Scopes          []string
}

// DefaultConfig configurazione di default
func DefaultConfig() Config {
	return Config{
		CredentialsFile: "credentials.json",
		TokenFile:       "token.json",
		Scopes: []string{
			drive.DriveFileScope,
			drive.DriveMetadataScope,
		},
	}
}

// NewClient crea un nuovo client Drive con supporto per il refresh automatico del token
func NewClient(ctx context.Context, config Config) (*Client, error) {
	credentials, err := os.ReadFile(config.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	_, err = google.ConfigFromJSON(credentials, config.Scopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	token, err := loadToken(config.TokenFile)
	if err != nil {
		logger.Warn("Token not found, need authentication", zap.Error(err))
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// Build OAuth config once
	oauthConfig, _ := google.ConfigFromJSON(credentials, config.Scopes...)

	// Use a background context for the token source.
	// This is critical: the token source needs its own long-lived context
	// so that token refresh works independently of any request context.
	tokenSource := oauthConfig.TokenSource(context.Background(), token)

	// Wrap with ReuseTokenSource to auto-save and refresh
	tokenSource = oauth2.ReuseTokenSource(token, &refreshingTokenSource{
		source:    tokenSource,
		tokenFile: config.TokenFile,
	})

	httpClient := oauth2.NewClient(context.Background(), tokenSource)

	service, err := drive.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return &Client{
		service:     service,
		tokenSource: tokenSource,
		tokenFile:   config.TokenFile,
		credsData:   credentials,
		scopes:      config.Scopes,
	}, nil
}

// refreshingTokenSource wraps a token source and saves refreshed tokens to file
type refreshingTokenSource struct {
	source    oauth2.TokenSource
	tokenFile string
	mu        sync.Mutex
}

// Token returns a valid token, refreshing if necessary, and saves to file
func (r *refreshingTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	token, err := r.source.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Save refreshed token to file for persistence across restarts
	if r.tokenFile != "" {
		if err := SaveToken(r.tokenFile, token); err != nil {
			logger.Warn("Failed to save refreshed token", zap.Error(err))
		} else {
			logger.Debug("Refreshed OAuth token saved successfully")
		}
	}

	return token, nil
}

// UploadVideo carica un video su Drive
func (c *Client) UploadVideo(ctx context.Context, videoPath, folderID, filename string) (string, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open video: %w", err)
	}
	defer file.Close()

	if filename == "" {
		filename = filepath.Base(videoPath)
	}

	driveFile := &drive.File{
		Name:    filename,
		Parents: []string{folderID},
	}

	result, err := c.service.Files.Create(driveFile).
		Media(file).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	logger.Info("Uploaded to Drive", zap.String("filename", filename), zap.String("id", result.Id))
	return result.Id, nil
}

// CreateFolder crea una cartella su Drive
func (c *Client) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	folder := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}

	result, err := c.service.Files.Create(folder).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create folder: %w", err)
	}

	logger.Info("Created Drive folder", zap.String("name", name), zap.String("id", result.Id))
	return result.Id, nil
}

// GetOrCreateFolder ottiene o crea una cartella
func (c *Client) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	query := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false and mimeType='application/vnd.google-apps.folder'", name, parentID)

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name)").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to search folder: %w", err)
	}

	if len(result.Files) > 0 {
		logger.Info("Found existing Drive folder", zap.String("name", name), zap.String("id", result.Files[0].Id))
		return result.Files[0].Id, nil
	}

	return c.CreateFolder(ctx, name, parentID)
}

// GetFolderByPath ottiene o crea una cartella percorso (es: "progetto/video/finali")
func (c *Client) GetFolderByPath(ctx context.Context, path string, rootFolderID string) (string, error) {
	if path == "" {
		return rootFolderID, nil
	}

	parts := filepath.SplitList(path)
	currentParentID := rootFolderID

	for _, part := range parts {
		if part == "" {
			continue
		}
		folderID, err := c.GetOrCreateFolder(ctx, part, currentParentID)
		if err != nil {
			return "", fmt.Errorf("failed to create folder %s: %w", part, err)
		}
		currentParentID = folderID
	}

	return currentParentID, nil
}

// ShareFile condivide un file
func (c *Client) ShareFile(ctx context.Context, fileID, email string, role string) error {
	permission := &drive.Permission{
		Type: "user",
		Role: role,
		EmailAddress: email,
	}

	_, err := c.service.Permissions.Create(fileID, permission).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to share file: %w", err)
	}

	logger.Info("Shared Drive file", zap.String("file_id", fileID), zap.String("email", email))
	return nil
}

// DeleteFile elimina un file
func (c *Client) DeleteFile(ctx context.Context, fileID string) error {
	err := c.service.Files.Delete(fileID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	logger.Info("Deleted Drive file", zap.String("file_id", fileID))
	return nil
}

// loadToken carica il token OAuth da file
func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try to parse as generic map first to handle different field names
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// Normalize field names for Go oauth2 library
	// Some tokens use "token" instead of "access_token"
	if accessToken, ok := raw["token"].(string); ok {
		raw["access_token"] = accessToken
		delete(raw, "token")
	}
	if tokenType, ok := raw["token_type"].(string); !ok || tokenType == "" {
		raw["token_type"] = "Bearer"
	}

	// Re-marshal with normalized names
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(normalized, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

// SaveToken salva il token OAuth su file
func SaveToken(path string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// ListFolders elenca le cartelle con supporto per ricorsione
func (c *Client) ListFolders(ctx context.Context, opts ListFoldersOptions) ([]Folder, error) {
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 2
	}
	if opts.MaxItems == 0 {
		opts.MaxItems = 50
	}

	folders, err := c.listFoldersRecursive(ctx, opts.ParentID, 0, opts.MaxDepth, opts.MaxItems)
	if err != nil {
		return nil, err
	}

	return folders, nil
}

// ListFoldersNoRecursion lists only immediate folders without recursion
func (c *Client) ListFoldersNoRecursion(ctx context.Context, opts ListFoldersOptions) ([]Folder, error) {
	if opts.MaxItems == 0 {
		opts.MaxItems = 50
	}

	var query string
	if opts.ParentID != "" {
		query = fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", opts.ParentID)
	} else {
		query = "mimeType='application/vnd.google-apps.folder' and trashed=false"
	}

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, parents)").
		PageSize(int64(opts.MaxItems)).
		OrderBy("name").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}

	var folders []Folder
	for _, f := range result.Files {
		folders = append(folders, Folder{
			ID:   f.Id,
			Name: f.Name,
			Link: f.WebViewLink,
		})
	}

	return folders, nil
}

// listFoldersRecursive helper ricorsivo per ListFolders
func (c *Client) listFoldersRecursive(ctx context.Context, parentID string, depth, maxDepth, maxItems int) ([]Folder, error) {
	if depth > maxDepth {
		return nil, nil
	}

	var query string
	if parentID != "" {
		query = fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", parentID)
	} else {
		query = "mimeType='application/vnd.google-apps.folder' and trashed=false"
	}

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, parents)").
		PageSize(int64(maxItems)).
		OrderBy("name").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}

	var folders []Folder
	for _, f := range result.Files {
		folder := Folder{
			ID:     f.Id,
			Name:   f.Name,
			Link:   f.WebViewLink,
			Depth:  depth,
		}

		// Get subfolders if not at max depth
		if depth < maxDepth {
			subfolders, err := c.listFoldersRecursive(ctx, f.Id, depth+1, maxDepth, 10)
			if err == nil && len(subfolders) > 0 {
				folder.Subfolders = subfolders
			}
		}

		folders = append(folders, folder)
	}

	return folders, nil
}

// GetFolderContent ottiene il contenuto di una cartella
func (c *Client) GetFolderContent(ctx context.Context, folderID string) (*FolderContent, error) {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)

	// Include videoMediaMetadata for duration/resolution
	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, mimeType, webViewLink, size, modifiedTime, videoMediaMetadata, createdTime)").
		PageSize(100).
		OrderBy("name").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get folder content: %w", err)
	}

	content := &FolderContent{
		FolderID: folderID,
	}

	for _, f := range result.Files {
		if f.MimeType == "application/vnd.google-apps.folder" {
			content.Subfolders = append(content.Subfolders, Folder{
				ID:   f.Id,
				Name: f.Name,
				Link: f.WebViewLink,
			})
		} else {
			file := File{
				ID:           f.Id,
				Name:         f.Name,
				MimeType:     f.MimeType,
				Link:         f.WebViewLink,
				Size:         f.Size,
				ModifiedTime: parseTime(f.ModifiedTime),
				CreatedTime:  parseTime(f.CreatedTime),
			}

			// Extract video metadata from Drive
			if f.VideoMediaMetadata != nil {
				file.DurationMs = f.VideoMediaMetadata.DurationMillis
				file.Width = f.VideoMediaMetadata.Width
				file.Height = f.VideoMediaMetadata.Height
			}

			content.Files = append(content.Files, file)
		}
	}

	content.TotalFolders = len(content.Subfolders)
	content.TotalFiles = len(content.Files)

	return content, nil
}

// GetFolderByName cerca una cartella per nome
func (c *Client) GetFolderByName(ctx context.Context, name, parentID string) (*Folder, error) {
	query := fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and name='%s' and trashed=false", name)
	if parentID != "" {
		query += fmt.Sprintf(" and '%s' in parents", parentID)
	}

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink)").
		PageSize(5).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to search folder: %w", err)
	}

	if len(result.Files) == 0 {
		return nil, fmt.Errorf("folder '%s' not found", name)
	}

	f := result.Files[0]
	return &Folder{
		ID:   f.Id,
		Name: f.Name,
		Link: f.WebViewLink,
	}, nil
}

// UploadFile carica un file generico su Drive
func (c *Client) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	if filename == "" {
		filename = filepath.Base(filePath)
	}

	driveFile := &drive.File{
		Name:    filename,
		Parents: []string{folderID},
	}

	result, err := c.service.Files.Create(driveFile).
		Media(file).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	logger.Info("Uploaded file to Drive", zap.String("filename", filename), zap.String("id", result.Id))
	return result.Id, nil
}

// SearchFiles cerca file per nome all'interno di una cartella
// Supporta ricerca parziale (contains) e filtra per tipo video
func (c *Client) SearchFiles(ctx context.Context, namePattern, folderID string, videoOnly bool) ([]File, error) {
	// Build query: file in folder with name containing pattern
	query := fmt.Sprintf("name contains '%s' and '%s' in parents and trashed=false", namePattern, folderID)
	if videoOnly {
		query += " and (mimeType contains 'video' or mimeType contains 'mp4' or mimeType contains 'quicktime')"
	}

	result, err := c.service.Files.List().
		Q(query).
		Fields("files(id, name, mimeType, webViewLink, size, modifiedTime, videoMediaMetadata, createdTime)").
		PageSize(20).
		OrderBy("modifiedTime desc").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to search files: %w", err)
	}

	var files []File
	for _, f := range result.Files {
		file := File{
			ID:           f.Id,
			Name:         f.Name,
			MimeType:     f.MimeType,
			Link:         f.WebViewLink,
			Size:         f.Size,
			ModifiedTime: parseTime(f.ModifiedTime),
			CreatedTime:  parseTime(f.CreatedTime),
		}

		if f.VideoMediaMetadata != nil {
			file.DurationMs = f.VideoMediaMetadata.DurationMillis
			file.Width = f.VideoMediaMetadata.Width
			file.Height = f.VideoMediaMetadata.Height
		}

		files = append(files, file)
	}

	return files, nil
}

// GetFile ottiene informazioni su un file
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	result, err := c.service.Files.Get(fileID).
		Fields("id, name, mimeType, webViewLink, size, modifiedTime, parents").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return &File{
		ID:           result.Id,
		Name:         result.Name,
		MimeType:     result.MimeType,
		Link:         result.WebViewLink,
		Size:         result.Size,
		ModifiedTime: parseTime(result.ModifiedTime),
		Parents:      result.Parents,
	}, nil
}

// DetectGroupFromTopic rileva il gruppo dal topic
func DetectGroupFromTopic(topic string) string {
	t := strings.ToLower(topic)
	switch {
	case containsAny(t, []string{"tech", "ai", "software", "spacex", "tesla"}):
		return "tech"
	case containsAny(t, []string{"business", "startup", "money", "elon", "musk"}):
		return "business"
	case containsAny(t, []string{"interview", "talk", "conversation", "podcast"}):
		return "interviews"
	case containsAny(t, []string{"news", "breaking"}):
		return "highlights"
	case containsAny(t, []string{"science", "research", "discovery"}):
		return "discovery"
	case containsAny(t, []string{"nature", "landscape", "wildlife"}):
		return "nature"
	case containsAny(t, []string{"city", "urban", "street"}):
		return "urban"
	default:
		return "general"
	}
}

// containsAny checks if string contains any of the substrings
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// parseTime parses Google Drive time format
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// GetDriveLink genera un link per un file/cartella
func GetDriveLink(fileID string) string {
	return fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)
}

// GetFolderLink genera un link per una cartella
func GetFolderLink(folderID string) string {
	return fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID)
}
