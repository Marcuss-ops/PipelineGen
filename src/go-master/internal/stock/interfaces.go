// Package stock provides stock video project management.
package stock

import (
	"context"
)

// Manager handles stock video project operations
type ManagerImpl interface {
	// Project management
	CreateProject(ctx context.Context, name string, config *ProjectConfig) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	ListProjects(ctx context.Context) ([]*Project, error)
	DeleteProject(ctx context.Context, name string) error

	// Download management
	StartDownload(ctx context.Context, projectID string, url string) (*DownloadTask, error)
	GetDownloadStatus(ctx context.Context, taskID string) (*DownloadTask, error)
	CancelDownload(ctx context.Context, taskID string) error
}
