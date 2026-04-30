package drive

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	googleDocsBaseURL = "https://docs.google.com/document/d/%s/edit"
)

// DocClient is an interface for Google Docs operations.
type DocClient interface {
	CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error)
}

// DocClientImpl is a Google Docs-backed DocClient.
type DocClientImpl struct {
	credentialsPath string
	tokenPath       string
	docsService     *docs.Service
	driveService    *drive.Service
}

// CreateDoc creates a new Google Doc, inserts the provided content, and moves it to the target folder when requested.
func (d *DocClientImpl) CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error) {
	if d.docsService == nil {
		return nil, fmt.Errorf("google docs service not initialized")
	}

	docTitle := strings.TrimSpace(title)
	if docTitle == "" {
		docTitle = "Untitled script"
	}

	created, err := d.docsService.Documents.Create(&docs.Document{
		Title: docTitle,
	}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create google doc: %w", err)
	}

	if err := d.insertContent(ctx, created.DocumentId, content); err != nil {
		return nil, err
	}

	if err := d.moveToFolder(ctx, created.DocumentId, folderID); err != nil {
		return nil, err
	}

	return &Doc{
		ID:      created.DocumentId,
		Title:   docTitle,
		URL:     fmt.Sprintf(googleDocsBaseURL, created.DocumentId),
		Content: content,
	}, nil
}

func (d *DocClientImpl) insertContent(ctx context.Context, docID, content string) error {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil
	}

	if _, err := d.docsService.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{
		Requests: []*docs.Request{
			{
				InsertText: &docs.InsertTextRequest{
					Location: &docs.Location{Index: 1},
					Text:     text,
				},
			},
		},
	}).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to insert google doc content: %w", err)
	}

	return nil
}

func (d *DocClientImpl) moveToFolder(ctx context.Context, docID, folderID string) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" || d.driveService == nil {
		return nil
	}

	file, err := d.driveService.Files.Get(docID).Fields("parents").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to fetch document parents: %w", err)
	}

	update := d.driveService.Files.Update(docID, nil).
		AddParents(folderID)
	if len(file.Parents) > 0 {
		update = update.RemoveParents(strings.Join(file.Parents, ","))
	}
	_, err = update.Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to move document to folder: %w", err)
	}

	return nil
}

// NewDocClient creates a new Google Docs-backed DocClient.
func NewDocClient(ctx context.Context, credentialsPath, tokenPath string) (DocClient, error) {
	if strings.TrimSpace(credentialsPath) == "" {
		return nil, fmt.Errorf("google credentials path is required")
	}
	if strings.TrimSpace(tokenPath) == "" {
		return nil, fmt.Errorf("google token path is required")
	}

	if _, err := os.Stat(credentialsPath); err != nil {
		return nil, fmt.Errorf("google credentials file not found: %w", err)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("google token file not found: %w", err)
	}

	httpClient, err := newGoogleHTTPClient(ctx, credentialsPath, tokenPath)
	if err != nil {
		return nil, err
	}

	docsService, err := docs.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize google docs service: %w", err)
	}

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize google drive service: %w", err)
	}

	return &DocClientImpl{
		credentialsPath: credentialsPath,
		tokenPath:       tokenPath,
		docsService:     docsService,
		driveService:    driveService,
	}, nil
}

func newGoogleHTTPClient(ctx context.Context, credentialsPath, tokenPath string) (*http.Client, error) {
	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read google credentials: %w", err)
	}

	cfg, err := googleoauth.ConfigFromJSON(credentials, docs.DocumentsScope, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse google credentials: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load google token: %w", err)
	}

	client := oauth2.NewClient(ctx, cfg.TokenSource(ctx, token))
	if client == nil {
		return nil, fmt.Errorf("failed to create google oauth client")
	}

	return client, nil
}
