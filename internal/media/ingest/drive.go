package ingest

import (
	"context"
	"strings"

	"go.uber.org/zap"

	driveutil "velox/go-master/internal/upload/drive"
)

func (s *Service) resolveDriveFolder(ctx context.Context, kind Kind, rootFolderID string, req *Request) (string, string, error) {
	if strings.TrimSpace(req.FolderID) != "" {
		return strings.TrimSpace(req.FolderID), strings.TrimSpace(req.FolderPath), nil
	}

	if s.driveUp == nil {
		return "", "", nil
	}

	if strings.TrimSpace(rootFolderID) == "" {
		zap.L().Warn("Drive root folder not configured, skipping Drive upload", zap.String("kind", string(kind)))
		return "", "", nil
	}

	var parts []string
	if path := strings.TrimSpace(req.FolderPath); path != "" {
		parts = splitFolderPath(path)
	} else {
		if group := strings.TrimSpace(req.Group); group != "" {
			parts = append(parts, group)
		} else if fallback := defaultGroupForKind(kind, req); fallback != "" {
			parts = append(parts, fallback)
		}
		if sub := strings.TrimSpace(req.Subfolder); sub != "" {
			parts = append(parts, sub)
		}
	}

	folderID, err := driveutil.EnsureFolderPath(ctx, s.driveUp, strings.TrimSpace(rootFolderID), parts...)
	if err != nil {
		return "", "", err
	}

	return folderID, strings.Join(parts, "/"), nil
}
