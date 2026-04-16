package drive

import (
	"context"
	"fmt"

	gdrive "google.golang.org/api/drive/v3"
)

// GetStartPageToken returns the current Drive change cursor for incremental sync.
func (c *Client) GetStartPageToken(ctx context.Context) (string, error) {
	if c == nil || c.service == nil {
		return "", fmt.Errorf("drive client not initialized")
	}
	resp, err := c.service.Changes.GetStartPageToken().SupportsAllDrives(true).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("get start page token: %w", err)
	}
	return resp.StartPageToken, nil
}

// ListChanges returns one page of changes since the provided cursor.
func (c *Client) ListChanges(ctx context.Context, pageToken string, pageSize int64) (*ChangeList, error) {
	if c == nil || c.service == nil {
		return nil, fmt.Errorf("drive client not initialized")
	}
	if pageToken == "" {
		return nil, fmt.Errorf("page token is required")
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	resp, err := c.service.Changes.List(pageToken).
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		RestrictToMyDrive(false).
		PageSize(pageSize).
		Fields("nextPageToken,newStartPageToken,changes(fileId,removed,time,driveId,changeType,file(id,name,mimeType,webViewLink,size,modifiedTime,createdTime,parents,videoMediaMetadata))").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("list drive changes: %w", err)
	}

	result := &ChangeList{
		NextPageToken:     resp.NextPageToken,
		NewStartPageToken: resp.NewStartPageToken,
		Changes:           make([]DriveChange, 0, len(resp.Changes)),
	}
	for _, change := range resp.Changes {
		result.Changes = append(result.Changes, mapDriveChange(change))
	}
	return result, nil
}

func mapDriveChange(change *gdrive.Change) DriveChange {
	mapped := DriveChange{
		ChangeID:   change.Id,
		FileID:     change.FileId,
		Removed:    change.Removed,
		Time:       parseTime(change.Time),
		DriveID:    change.DriveId,
		ChangeType: change.ChangeType,
	}
	if change.File != nil {
		mapped.File = &File{
			ID:           change.File.Id,
			Name:         change.File.Name,
			MimeType:     change.File.MimeType,
			Link:         change.File.WebViewLink,
			Size:         change.File.Size,
			ModifiedTime: parseTime(change.File.ModifiedTime),
			CreatedTime:  parseTime(change.File.CreatedTime),
			Parents:      change.File.Parents,
		}
		if change.File.VideoMediaMetadata != nil {
			mapped.File.DurationMs = change.File.VideoMediaMetadata.DurationMillis
			mapped.File.Width = change.File.VideoMediaMetadata.Width
			mapped.File.Height = change.File.VideoMediaMetadata.Height
		}
	}
	return mapped
}
