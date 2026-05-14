package artlist

import (
"context"
"fmt"
"path"
"strings"

driveapi "google.golang.org/api/drive/v3"

driveutil "velox/go-master/pkg/drive"
)

// DestinationInfo rappresenta una destinazione risolta per i clip
type DestinationInfo struct {
FolderID   string
FolderPath string
}

// DestinationService risolve le destinazioni Drive per i clip
type DestinationService struct {
svc *Service
}

// NewDestinationService crea un nuovo servizio di destinazione
func NewDestinationService(svc *Service) *DestinationService {
return &DestinationService{svc: svc}
}

// ResolveDestination risolve la cartella Drive per un termine
func (d *DestinationService) ResolveDestination(ctx context.Context, term, rootFolderID string) (*DestinationInfo, error) {
if term == "" {
return nil, fmt.Errorf("term is required")
}

if rootFolderID == "" {
return nil, fmt.Errorf("root folder ID is required")
}

folderName := sanitizeFolderName(term)
folderPath := path.Join("/Artlist", folderName)

existingID, err := d.findExistingFolder(ctx, rootFolderID, folderName)
if err != nil {
return nil, fmt.Errorf("failed to search for folder: %w", err)
}

if existingID != "" {
return &DestinationInfo{
FolderID:   existingID,
FolderPath: folderPath,
}, nil
}

newID, err := d.createFolder(ctx, rootFolderID, folderName)
if err != nil {
return nil, fmt.Errorf("failed to create folder: %w", err)
}

return &DestinationInfo{
FolderID:   newID,
FolderPath: folderPath,
}, nil
}

func (d *DestinationService) findExistingFolder(ctx context.Context, parentID, name string) (string, error) {
query := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", 
strings.ReplaceAll(name, "'", "\\'"), parentID)

files, err := d.svc.driveSvc.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
if err != nil {
return "", err
}

if len(files.Files) > 0 {
return files.Files[0].Id, nil
}

return "", nil
}

func (d *DestinationService) createFolder(ctx context.Context, parentID, name string) (string, error) {
folder := &driveapi.File{
Name:     name,
MimeType: "application/vnd.google-apps.folder",
Parents:  []string{parentID},
}

result, err := d.svc.driveSvc.Files.Create(folder).Fields("id").Context(ctx).Do()
if err != nil {
return "", err
}

return result.Id, nil
}

func sanitizeFolderName(name string) string {
name = strings.TrimSpace(name)
name = strings.ReplaceAll(name, "/", "-")
name = strings.ReplaceAll(name, "\\", "-")
name = strings.ReplaceAll(name, ":", "-")
name = strings.ReplaceAll(name, "*", "-")
name = strings.ReplaceAll(name, "?", "-")
name = strings.ReplaceAll(name, "\"", "-")
name = strings.ReplaceAll(name, "<", "-")
name = strings.ReplaceAll(name, ">", "-")
name = strings.ReplaceAll(name, "|", "-")
return strings.TrimSpace(name)
}
