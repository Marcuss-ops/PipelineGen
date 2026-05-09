package artlist

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/core/destination"
)

// resolveDestination resolves the Drive folder for the tag
func (s *Service) resolveDestination(ctx context.Context, rootFolderID, term, tagFolderName string, resp *RunTagResponse) string {
	tagFolderID := rootFolderID
	if s.assetDestResolver != nil && rootFolderID != "" {
		resolved, err := s.assetDestResolver.Resolve(ctx, &destination.ResolveRequest{
			Source:          "artlist",
			Group:           term,
			FolderID:        rootFolderID,
			SubfolderName:   tagFolderName,
			CreateSubfolder: true,
		})
		if err != nil {
			s.log.Warn("failed to resolve drive destination, using root folder ID",
				zap.String("root_folder_id", rootFolderID),
				zap.Error(err),
			)
		} else {
			tagFolderID = resolved.FolderID
		}
	}
	if !resp.DryRun && s.assetDestResolver != nil && tagFolderID != "" {
		s.log.Info("using artlist folder for uploads",
			zap.String("folder_id", tagFolderID),
			zap.String("folder_link", "https://drive.google.com/drive/folders/"+tagFolderID),
		)
	}
	return tagFolderID
}
