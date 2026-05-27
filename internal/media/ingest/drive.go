package ingest

import (
	"context"
	"strings"

	"go.uber.org/zap"
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

	folderID := strings.TrimSpace(rootFolderID)
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

	for _, part := range parts {
		nextID, err := s.driveUp.GetOrCreateFolder(ctx, part, folderID)
		if err != nil {
			return "", "", err
		}
		folderID = nextID
	}

	return folderID, strings.Join(parts, "/"), nil
}
