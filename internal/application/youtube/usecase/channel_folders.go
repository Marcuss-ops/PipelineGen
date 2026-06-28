// channel_folders.go — Drive folder resolution for YouTube channels.
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// GetOrCreateChannelFolder resolves the Drive folder for a channel via
// the DriveFolderManagerPort. The previous dummy fallback to a raw
// driveclient (concrete Drive SDK) has been removed.
func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return parentFolderID, fmt.Errorf("youtube: drive folder manager not wired — composition root must include DriveFolderMgr in ServiceDeps")
	}
	folderID, err := s.driveFolderMgr.GetOrCreateFolder(ctx, channelName, parentFolderID)
	if err != nil {
		return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
	}
	s.log.Info("channel folder resolved",
		zap.String("channel", channelName),
		zap.String("folder_id", folderID),
		zap.String("parent", parentFolderID))
	return folderID, nil
}
