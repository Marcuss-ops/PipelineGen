// Package drive provides Google Docs integration.
package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// DocClient gestisce le operazioni Google Docs
type DocClient struct {
	docsService *docs.Service
	driveClient *Client
}

// NewDocClient crea un nuovo client Docs
func NewDocClient(ctx context.Context, driveClient *Client, credentialsFile, tokenFile string) (*DocClient, error) {
	credentials, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	token, err := loadToken(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	oauthConfig, err := google.ConfigFromJSON(credentials, docs.DocumentsScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	// Use context.Background() for the token source.
	// This is critical: the token source needs its own long-lived context
	// so that token refresh works independently of any request context.
	tokenSource := oauthConfig.TokenSource(context.Background(), token)

	docsService, err := docs.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create Docs service: %w", err)
	}

	return &DocClient{
		docsService: docsService,
		driveClient: driveClient,
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

	// Add content if provided
	if content != "" {
		if err := d.AppendToDoc(ctx, docID, content); err != nil {
			logger.Warn("Failed to add content to doc", zap.Error(err))
		}
	}

	// Move to folder if specified
	if folderID != "" && d.driveClient != nil {
		_, err := d.driveClient.service.Files.Update(docID, nil).
			AddParents(folderID).
			Context(ctx).
			Do()
		if err != nil {
			logger.Warn("Failed to move doc to folder", zap.Error(err))
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

// loadCredentials loads OAuth credentials from file
func loadCredentials(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// createOAuthConfig creates OAuth config from credentials
func createOAuthConfig(credentials []byte, scopes []string) (*oauth2.Config, error) {
	return google.ConfigFromJSON(credentials, scopes...)
}

// SaveToken saves OAuth token to file (re-export for convenience)
func saveToken(path string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
