package drive

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/drive/v3"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// DetectGroupFromTopic maps a topic string to a canonical folder group (e.g., "Boxe", "Music", "Wwe").
func DetectGroupFromTopic(topic string) string {
	topic = strings.ToLower(topic)
	switch {
	case strings.Contains(topic, "boxe"), strings.Contains(topic, "boxing"), strings.Contains(topic, "fight"),
		strings.Contains(topic, "mayweather"), strings.Contains(topic, "tyson"), strings.Contains(topic, "gervonta"):
		return "Boxe"
	case strings.Contains(topic, "wwe"), strings.Contains(topic, "wrestling"), strings.Contains(topic, "raw"),
		strings.Contains(topic, "smackdown"), strings.Contains(topic, "royal rumble"):
		return "Wwe"
	case strings.Contains(topic, "music"), strings.Contains(topic, "song"), strings.Contains(topic, "lyrics"),
		strings.Contains(topic, "rapper"), strings.Contains(topic, "official video"):
		return "Music"
	case strings.Contains(topic, "crime"), strings.Contains(topic, "arrest"), strings.Contains(topic, "mafia"),
		strings.Contains(topic, "gang"), strings.Contains(topic, "cartel"):
		return "Crime"
	case strings.Contains(topic, "discovery"), strings.Contains(topic, "documentary"), strings.Contains(topic, "science"),
		strings.Contains(topic, "education"), strings.Contains(topic, "history"):
		return "Discovery"
	case strings.Contains(topic, "hiphop"), strings.Contains(topic, "rap"), strings.Contains(topic, "hip hop"):
		return "HipHop"
	default:
		return "Various"
	}
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
	// Escape single quotes in the folder name to prevent query syntax errors
	escapedName := strings.ReplaceAll(name, "'", "\\'")

	query := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false and mimeType='application/vnd.google-apps.folder'", escapedName, parentID)

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

	parts := strings.Split(path, "/")
	currentParentID := rootFolderID

	for _, part := range parts {
		part = strings.TrimSpace(part)
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

	ctx = c.withTimeout(ctx)

	var query string
	if opts.ParentID != "" {
		query = fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", opts.ParentID)
	} else {
		query = "mimeType='application/vnd.google-apps.folder' and trashed=false"
	}

	var result *drive.FileList
	err := c.withRetry(ctx, func() error {
		var err error
		result, err = c.service.Files.List().
			Q(query).
			Fields("files(id, name, webViewLink, parents)").
			PageSize(int64(opts.MaxItems)).
			OrderBy("name").
			Context(ctx).
			Do()
		return err
	})
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
			ID:    f.Id,
			Name:  f.Name,
			Link:  f.WebViewLink,
			Depth: depth,
		}

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

	ctx = c.withTimeout(ctx)

	var result *drive.FileList
	err := c.withRetry(ctx, func() error {
		var err error
		result, err = c.service.Files.List().
			Q(query).
			Fields("files(id, name, mimeType, webViewLink, size, modifiedTime, videoMediaMetadata, createdTime)").
			PageSize(100).
			OrderBy("name").
			Context(ctx).
			Do()
		return err
	})
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
