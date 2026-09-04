package wiring

import (
	"context"
	"fmt"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// driveAdminAdapter bridges the system capability's narrow admin port to the
// canonical Drive admin/read/lifecycle ports. Concrete Drive ownership remains
// in internal/platform/drive; this file is composition-only glue.
type driveAdminAdapter struct {
	admin     drive.Admin
	reader    drive.Reader
	lifecycle drive.FileLifecycle
	log       *zap.Logger
}

var _ systemapi.DriveAdminOps = (*driveAdminAdapter)(nil)

func newDriveAdminAdapter(admin drive.Admin, reader drive.Reader, lifecycle drive.FileLifecycle, log *zap.Logger) systemapi.DriveAdminOps {
	if admin == nil {
		return nil
	}
	return &driveAdminAdapter{admin: admin, reader: reader, lifecycle: lifecycle, log: log}
}

func (a *driveAdminAdapter) GetOrCreateFolder(ctx context.Context, folderName, parentID string) (string, error) {
	return a.admin.GetOrCreateFolder(ctx, folderName, parentID)
}

func (a *driveAdminAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.admin.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *driveAdminAdapter) ListFiles(ctx context.Context, folderID string) ([]systemapi.DriveFileInfoDTO, error) {
	files, err := a.reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]systemapi.DriveFileInfoDTO, len(files))
	for i, f := range files {
		out[i] = systemapi.DriveFileInfoDTO{
			ID:             f.ID,
			Name:           f.Name,
			MimeType:       f.MimeType,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		}
	}
	return out, nil
}

func (a *driveAdminAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	if a.lifecycle == nil {
		return fmt.Errorf("driveAdminAdapter: lifecycle not wired (P1-5 CUTOVER requires FileLifecycle)")
	}
	return a.lifecycle.Rename(ctx, fileID, newName)
}

func (a *driveAdminAdapter) ResolveFileInfo(ctx context.Context, fileID string) (systemapi.ResolveByIDsItem, error) {
	if a.reader == nil {
		return systemapi.ResolveByIDsItem{}, fmt.Errorf("driveAdminAdapter: reader not wired")
	}
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("system_adapters driveAdminAdapter.ResolveFileInfo: GetFileMeta failed",
				zap.String("file_id", fileID), zap.Error(err))
		}
		return systemapi.ResolveByIDsItem{}, err
	}
	if meta == nil {
		return systemapi.ResolveByIDsItem{}, nil
	}
	return systemapi.ResolveByIDsItem{
		ID:          meta.ID,
		Name:        meta.Name,
		MimeType:    meta.MimeType,
		Parents:     meta.Parents,
		WebViewLink: meta.WebViewLink,
		Size:        meta.Size,
		Trashed:     meta.Trashed,
	}, nil
}
