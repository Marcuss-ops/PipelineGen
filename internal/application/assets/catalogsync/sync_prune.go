package catalogsync

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

func (s *Service) pruneMissingFolders(ctx context.Context, repo CatalogRepository, source string, seenFolderIDs map[string]struct{}) error {
	if repo == nil {
		return nil
	}

	folders, err := repo.ListFolders(ctx, source)
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
		if err := repo.DeleteFolder(ctx, folder.ID); err != nil {
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
