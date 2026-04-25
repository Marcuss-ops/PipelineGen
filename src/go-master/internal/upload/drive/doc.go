// Package drive provides Google Docs integration.
package drive

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// DocClient gestisce le operazioni Google Docs e Drive
type DocClient struct {
	docsService  *docs.Service
	driveService *drive.Service
}

// NewDocClient crea un nuovo client Docs e Drive.
func NewDocClient(ctx context.Context, credentialsFile, tokenFile string) (*DocClient, error) {
	credentials, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	token, err := loadToken(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// Request both Docs and Drive scopes
	oauthConfig, err := google.ConfigFromJSON(credentials, docs.DocumentsScope, drive.DriveFileScope, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	tokenSource := oauthConfig.TokenSource(context.Background(), token)

	docsService, err := docs.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create Docs service: %w", err)
	}

	driveService, err := drive.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return &DocClient{
		docsService:  docsService,
		driveService: driveService,
	}, nil
}

// CreateDoc crea un nuovo Google Doc
func (d *DocClient) CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error) {
	doc := &docs.Document{
		Title: title,
	}

	result, err := d.docsService.Documents.Create(doc).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	docID := result.DocumentId
	docURL := fmt.Sprintf("https://docs.google.com/document/d/%s/edit", docID)

	logger.Info("Created Google Doc", zap.String("title", title), zap.String("id", docID))

	// Move to folder if specified
	if folderID != "" {
		// Retreive existing parents to remove them
		file, err := d.driveService.Files.Get(docID).Fields("parents").Context(ctx).Do()
		if err == nil && len(file.Parents) > 0 {
			previousParents := strings.Join(file.Parents, ",")
			_, err = d.driveService.Files.Update(docID, nil).
				AddParents(folderID).
				RemoveParents(previousParents).
				Context(ctx).Do()
			if err != nil {
				logger.Warn("Failed to move document to target folder", zap.Error(err), zap.String("folder", folderID))
			} else {
				logger.Info("Moved Google Doc to folder", zap.String("doc_id", docID), zap.String("folder", folderID))
			}
		} else {
			logger.Warn("Could not get parents of created doc for moving", zap.Error(err))
		}
	}

	// Add content if provided
	if content != "" {
		if err := d.AppendToDoc(ctx, docID, content); err != nil {
			logger.Warn("Failed to add content to doc", zap.Error(err))
		}
	}

	return &Doc{
		ID:    docID,
		Title: title,
		URL:   docURL,
	}, nil
}

// AppendToDoc aggiunge contenuto a un documento esistente
func (d *DocClient) AppendToDoc(ctx context.Context, docID, content string) error {
	const chunkSize = 1800
	runes := []rune(content)
	totalChunks := (len(runes) + chunkSize - 1) / chunkSize
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		requests := []*docs.Request{
			{
				InsertText: &docs.InsertTextRequest{
					EndOfSegmentLocation: &docs.EndOfSegmentLocation{
						SegmentId: "",
					},
					Text: chunk,
				},
			},
		}

		_, err := d.docsService.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{
			Requests: requests,
		}).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to append to document chunk %d/%d: %w", i/chunkSize+1, totalChunks, err)
		}
	}

	logger.Info("Appended content to doc", zap.String("doc_id", docID), zap.Int("chars", len(content)), zap.Int("chunks", totalChunks))
	return nil
}

// GetDocContent ottiene il contenuto di un documento
func (d *DocClient) GetDocContent(ctx context.Context, docID string) (string, error) {
	doc, err := d.docsService.Documents.Get(docID).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to get document: %w", err)
	}

	var content strings.Builder
	for _, element := range doc.Body.Content {
		if element.Paragraph != nil {
			for _, textRun := range element.Paragraph.Elements {
				if textRun.TextRun != nil {
					content.WriteString(textRun.TextRun.Content)
				}
			}
		}
	}

	return content.String(), nil
}

// AppendToDocByURL aggiunge contenuto usando l'URL del documento
func (d *DocClient) AppendToDocByURL(ctx context.Context, docURL, content string) error {
	docID := ExtractDocIDFromURL(docURL)
	if docID == "" {
		return fmt.Errorf("invalid document URL")
	}
	return d.AppendToDoc(ctx, docID, content)
}

// ExtractDocIDFromURL estrae l'ID del documento dall'URL
func ExtractDocIDFromURL(url string) string {
	const prefix = "/document/d/"
	idx := strings.Index(url, prefix)
	if idx == -1 {
		return ""
	}

	start := idx + len(prefix)
	end := strings.Index(url[start:], "/")
	if end == -1 {
		return url[start:]
	}
	return url[start : start+end]
}
