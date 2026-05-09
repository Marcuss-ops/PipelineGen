package assettree

import (
	"context"
	"path"
	"strings"

	repo "velox/go-master/internal/repository/assettree"
	"go.uber.org/zap"
)

// Service provides utility functions for asset trees
type Service struct {
	repo *repo.Repository
	log  *zap.Logger
}

// NewService creates a new asset tree service
func NewService(r *repo.Repository, log *zap.Logger) *Service {
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
			link = "https://drive.google.com/drive/folders/" + id
		} else {
			link = "https://drive.google.com/file/d/" + id + "/view"
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

// ListChildren gets the direct children of a given parent node
func (s *Service) ListChildren(ctx context.Context, source, parentID string) ([]*repo.AssetNode, error) {
	return s.repo.GetChildren(ctx, source, parentID)
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
