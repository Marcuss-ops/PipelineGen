package assets

import (
	"context"
	"path"
	"strings"

	repo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// Service provides utility functions for asset trees
type Service struct {
	repo *repo.AssetTreeRepository
	log  *zap.Logger
}

// NewService creates a new asset tree service
func NewService(r *repo.AssetTreeRepository, log *zap.Logger) *Service {
	return &Service{
		repo: r,
		log:  log,
	}
}

// IsFolderMime checks if a given mimetype represents a Google Drive folder
func (s *Service) IsFolderMime(mimeType string) bool {
	return mimeType == "application/vnd.google-apps.folder"
}

// ComputeDepth computes the depth of a node based on its path.
// Root level (no slashes) returns 0.
func (s *Service) ComputeDepth(nodePath string) int {
	cleanPath := strings.Trim(nodePath, "/")
	if cleanPath == "" {
		return 0
	}
	return strings.Count(cleanPath, "/")
}

// NormalizeDriveNode creates an AssetNode from raw drive attributes
func (s *Service) NormalizeDriveNode(
	id, name, mimeType, webViewLink, webContentLink, parentID, rootID, parentPath, source, assetID string,
) *repo.AssetNode {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = id
	}

	nodePath := cleanName
	if parentPath != "" {
		nodePath = path.Join(parentPath, cleanName)
	}

	link := strings.TrimSpace(webViewLink)
	if link == "" {
		link = strings.TrimSpace(webContentLink)
	}
	isFolder := s.IsFolderMime(mimeType)

	if link == "" {
		if isFolder {
			link = drive.FolderURLFromID(id)
		} else {
			link = drive.FileURLFromID(id)
		}
	}

	nodeType := "file"
	if isFolder {
		nodeType = "folder"
	} else if strings.HasPrefix(mimeType, "video/") {
		nodeType = "video"
	} else if strings.HasPrefix(mimeType, "audio/") {
		nodeType = "audio"
	} else if strings.HasPrefix(mimeType, "image/") {
		nodeType = "image"
	}

	return &repo.AssetNode{
		ID:          id,
		Source:      source,
		AssetID:     assetID,
		Name:        cleanName,
		Type:        nodeType,
		ParentID:    parentID,
		RootID:      rootID,
		Path:        nodePath,
		Depth:       s.ComputeDepth(nodePath),
		IsFolder:    isFolder,
		DriveFileID: id,
		DriveLink:   link,
		Metadata:    "{}",
	}
}

// UpsertNode persists a node using the repository
func (s *Service) UpsertNode(ctx context.Context, node *repo.AssetNode) error {
	return s.repo.UpsertNode(ctx, node)
}

// DeleteByAssetID deletes a node by its source and original asset ID.
func (s *Service) DeleteByAssetID(ctx context.Context, source, assetID string) error {
	return s.repo.DeleteByAssetID(ctx, source, assetID)
}

// DeleteNode deletes a node by its explicit ID.
func (s *Service) DeleteNode(ctx context.Context, id string) error {
	return s.repo.DeleteNode(ctx, id)
}

// ListChildren gets the direct children of a given parent node
func (s *Service) ListChildren(ctx context.Context, source, parentID string) ([]*repo.AssetNode, error) {
	return s.ListChildrenPaged(ctx, source, parentID, 10000, 0)
}

// ListChildrenPaged gets the direct children of a given parent node with pagination
func (s *Service) ListChildrenPaged(ctx context.Context, source, parentID string, limit, offset int) ([]*repo.AssetNode, error) {
	return s.repo.GetChildrenPaged(ctx, source, parentID, limit, offset)
}

// GetBreadcrumb returns the path from root to the given node ID
func (s *Service) GetBreadcrumb(ctx context.Context, id string) ([]*repo.AssetNode, error) {
	var breadcrumb []*repo.AssetNode

	currentID := id
	for currentID != "" {
		node, err := s.repo.GetNode(ctx, currentID)
		if err != nil {
			// If we fail to fetch a parent, just break and return what we have
			break
		}
		// Prepend to breadcrumb
		breadcrumb = append([]*repo.AssetNode{node}, breadcrumb...)
		currentID = node.ParentID
	}

	return breadcrumb, nil
}

// FolderInfo is a lightweight result for style-folder lookups.
type FolderInfo struct {
	Name       string `json:"name"`
	FolderID   string `json:"folder_id"`
	DriveLink  string `json:"drive_link"`
	Path       string `json:"path"`
	ChildCount int    `json:"child_count"`
}

// ResolveStyleFolder looks up a style folder by name under a given root in the asset tree.
// It first searches direct children of rootID, then falls back to a deeper path search.
// Returns nil if not found.
func (s *Service) ResolveStyleFolder(ctx context.Context, source, rootID, styleName string) (*FolderInfo, error) {
	if rootID == "" || styleName == "" {
		return nil, nil
	}

	// 1. Try direct children of root (most common: style folders are depth-1)
	node, err := s.repo.FindByName(ctx, source, rootID, rootID, styleName)
	if err == nil && node != nil {
		return &FolderInfo{
			Name:       node.Name,
			FolderID:   node.DriveFileID,
			DriveLink:  node.DriveLink,
			Path:       node.Path,
			ChildCount: node.ChildCount,
		}, nil
	}

	// 2. Broader search: list all nodes under root and match by name (handles deeper nesting)
	nodes, err := s.repo.ListByRoot(ctx, source, rootID)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.IsFolder && strings.EqualFold(n.Name, styleName) {
			return &FolderInfo{
				Name:       n.Name,
				FolderID:   n.DriveFileID,
				DriveLink:  n.DriveLink,
				Path:       n.Path,
				ChildCount: n.ChildCount,
			}, nil
		}
	}

	return nil, nil
}

// ResolveStyleFolders returns all direct style subfolders under a root.
func (s *Service) ResolveStyleFolders(ctx context.Context, source, rootID string) ([]FolderInfo, error) {
	if rootID == "" {
		return nil, nil
	}

	nodes, err := s.repo.GetChildren(ctx, source, rootID)
	if err != nil {
		return nil, err
	}

	var folders []FolderInfo
	for _, n := range nodes {
		if n.IsFolder {
			folders = append(folders, FolderInfo{
				Name:       n.Name,
				FolderID:   n.DriveFileID,
				DriveLink:  n.DriveLink,
				Path:       n.Path,
				ChildCount: n.ChildCount,
			})
		}
	}
	return folders, nil
}
