package drive

import "context"

// DocClient is an interface for Google Docs operations
type DocClient interface {
	CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error)
}

// DocClientImpl is a minimal implementation of DocClient
type DocClientImpl struct {
	credentialsPath string
	tokenPath      string
}

// CreateDoc creates a new Google Doc (minimal implementation)
func (d *DocClientImpl) CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error) {
	return &Doc{
		ID:      "placeholder",
		Title:   title,
		URL:     "",
		Content: content,
	}, nil
}

// NewDocClient creates a new DocClient
func NewDocClient(ctx context.Context, credentialsPath, tokenPath string) (DocClient, error) {
	return &DocClientImpl{
		credentialsPath: credentialsPath,
		tokenPath:      tokenPath,
	}, nil
}
