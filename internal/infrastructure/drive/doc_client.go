package drive

import (
	"context"
	"fmt"
	"os"
	"strings"

	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	googleDocsBaseURL = "https://docs.google.com/document/d/%s/edit"
)

func googleDocsEditURL(docID string) string {
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return ""
	}
	return fmt.Sprintf(googleDocsBaseURL, docID)
}

// DocClient is an interface for Google Docs operations.
// The canonical `Doc` DTO is declared in types.go (same package) so
// every application caller can `*drive.Doc` through this interface
// without re-declaring the struct.
type DocClient interface {
	CreateDoc(ctx context.Context, title, content, folderID string) (*Doc, error)
	CreateDocIdempotent(ctx context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (*Doc, error)
	ShareDoc(ctx context.Context, docID, email, role string) error
	ListRecentDocs(ctx context.Context, folderID string, limit int) ([]Doc, error)
	UpdateDoc(ctx context.Context, docID, title, content string) error
}

// DocumentReader is the narrow read-only Google Docs retrieval port.
// It is intentionally separate from DocClient so callers that only need
// document inspection do not force every document mock to expose the Docs
// API structural type.
type DocumentReader interface {
	GetDocument(ctx context.Context, docID string) (*docs.Document, error)
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

	// HTML content is built structurally via the Docs API so the title
	// heading and code blocks survive. Drive's HTML import silently drops
	// both, so it must not be used for the canonical document surface.
	if isHTMLContent(content) {
		if d.docsService == nil {
			return nil, fmt.Errorf("google docs service not initialized")
		}

		created, err := d.docsService.Documents.Create(&docs.Document{
			Title: docTitle,
		}).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("failed to create google doc: %w", err)
		}

		if err := d.insertHTMLContent(ctx, created.DocumentId, content); err != nil {
			return nil, err
		}

		if err := d.moveToFolder(ctx, created.DocumentId, folderID); err != nil {
			return nil, err
		}

		return &Doc{
			ID:      created.DocumentId,
			Title:   docTitle,
			URL:     googleDocsEditURL(created.DocumentId),
			Content: content,
		}, nil
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
		URL:     googleDocsEditURL(created.DocumentId),
		Content: content,
	}, nil
}

// CreateDocIdempotent creates a Google Doc only once for a given
// idempotency key. When a doc with the same key already exists, it
// returns the existing doc unless forceRefresh is true, in which case
// the existing doc content is overwritten. The key is stored as a Drive
// app property named "pipelinegen_generation_id".
func (d *DocClientImpl) CreateDocIdempotent(ctx context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (*Doc, error) {
	if idempotencyKey == "" {
		return d.CreateDoc(ctx, title, content, folderID)
	}

	existing, err := d.findDocByIdempotencyKey(ctx, folderID, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup existing doc: %w", err)
	}

	if existing != nil {
		if !forceRefresh {
			return existing, nil
		}
		if err := d.UpdateDoc(ctx, existing.ID, title, content); err != nil {
			return nil, fmt.Errorf("failed to refresh existing doc: %w", err)
		}
		existing.Title = title
		existing.Content = content
		return existing, nil
	}

	doc, err := d.CreateDoc(ctx, title, content, folderID)
	if err != nil {
		return nil, err
	}
	if err := d.setAppProperty(ctx, doc.ID, "pipelinegen_generation_id", idempotencyKey); err != nil {
		// Non-fatal: the doc exists but we could not tag it. Log is
		// not available here, so surface as a wrapped error but still
		// return the created doc so callers do not lose the link.
		return doc, fmt.Errorf("doc created but idempotency tag failed: %w", err)
	}
	return doc, nil
}

func (d *DocClientImpl) findDocByIdempotencyKey(ctx context.Context, folderID, key string) (*Doc, error) {
	if d.driveService == nil {
		return nil, fmt.Errorf("drive service not initialized")
	}
	// Escape single quotes in the key to keep the Drive query valid.
	safeKey := strings.ReplaceAll(key, "'", "\\'")
	q := fmt.Sprintf("appProperties has { key='pipelinegen_generation_id' and value='%s' }", safeKey)
	if folderID != "" {
		q = fmt.Sprintf("'%s' in parents and %s", folderID, q)
	}

	files, err := d.driveService.Files.List().
		Q(q).
		PageSize(1).
		Fields("files(id, name, webViewLink)").
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to search doc by idempotency key: %w", err)
	}
	if len(files.Files) == 0 {
		return nil, nil
	}
	f := files.Files[0]
	return &Doc{
		ID:    f.Id,
		Title: f.Name,
		URL:   googleDocsEditURL(f.Id),
	}, nil
}

func (d *DocClientImpl) setAppProperty(ctx context.Context, docID, key, value string) error {
	if d.driveService == nil {
		return fmt.Errorf("drive service not initialized")
	}
	_, err := d.driveService.Files.Update(docID, &drive.File{
		AppProperties: map[string]string{key: value},
	}).Fields("id").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to set app property: %w", err)
	}
	return nil
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

// insertHTMLContent builds the canonical document HTML structurally via the
// Docs API, preserving the title heading and the SpecScene JSON code block
// that Drive's HTML import would otherwise drop.
func (d *DocClientImpl) insertHTMLContent(ctx context.Context, docID, content string) error {
	reqs, err := platformdrive.BuildInsertRequests(content)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return nil
	}
	if _, err := d.docsService.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{Requests: reqs}).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to insert document html content: %w", err)
	}
	return nil
}

// clearDocumentBody removes all body content from a document so a subsequent
// insert starts from a clean slate.
func (d *DocClientImpl) clearDocumentBody(ctx context.Context, docID string) error {
	document, err := d.docsService.Documents.Get(docID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to read google doc before update: %w", err)
	}
	if endIndex := bodyEndIndex(document); endIndex > 2 {
		if _, err := d.docsService.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{
			Requests: []*docs.Request{
				{DeleteContentRange: &docs.DeleteContentRangeRequest{Range: &docs.Range{StartIndex: 1, EndIndex: endIndex - 1}}},
			},
		}).Context(ctx).Do(); err != nil {
			return fmt.Errorf("failed to clear google doc content: %w", err)
		}
	}
	return nil
}

func bodyEndIndex(document *docs.Document) int64 {
	if document == nil || document.Body == nil || len(document.Body.Content) == 0 {
		return 1
	}
	return document.Body.Content[len(document.Body.Content)-1].EndIndex
}

// ShareDoc shares a Google Doc with a specific user by email.
func (d *DocClientImpl) ShareDoc(ctx context.Context, docID, email, role string) error {
	email = strings.TrimSpace(email)
	if email == "" || d.driveService == nil {
		return nil
	}
	if role == "" {
		role = "writer"
	}

	perm := &drive.Permission{
		Type:         "user",
		Role:         role,
		EmailAddress: email,
	}
	_, err := d.driveService.Permissions.Create(docID, perm).
		SendNotificationEmail(true).
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("failed to share google doc with %s: %w", email, err)
	}
	return nil
}

// ListRecentDocs returns recent Google Docs created in the given folder (or across Drive if folderID is empty).
func (d *DocClientImpl) ListRecentDocs(ctx context.Context, folderID string, limit int) ([]Doc, error) {
	if d.driveService == nil {
		return nil, fmt.Errorf("drive service not initialized")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	q := "mimeType='application/vnd.google-apps.document'"
	folderID = strings.TrimSpace(folderID)
	if folderID != "" {
		q = fmt.Sprintf("'%s' in parents and %s", folderID, q)
	}

	files, err := d.driveService.Files.List().
		Q(q).
		OrderBy("createdTime desc").
		PageSize(int64(limit)).
		Fields("files(id, name, createdTime, webViewLink)").
		Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list recent docs: %w", err)
	}

	var docs []Doc
	for _, f := range files.Files {
		docs = append(docs, Doc{
			ID:        f.Id,
			Title:     f.Name,
			URL:       googleDocsEditURL(f.Id),
			CreatedAt: f.CreatedTime,
		})
	}
	return docs, nil
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

	httpClient, err := NewGoogleHTTPClient(ctx, credentialsPath, tokenPath, docs.DocumentsScope, drive.DriveScope)
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

// GetDocument retrieves the complete structural document, including
// multi-tab content, for read-only auditing and diagnostics. With
// includeTabsContent enabled, the Docs API returns content under Tabs;
// the audit walker intentionally uses that representation instead of
// duplicating legacy top-level content.
func (d *DocClientImpl) GetDocument(ctx context.Context, docID string) (*docs.Document, error) {
	if d.docsService == nil {
		return nil, fmt.Errorf("google docs service not initialized")
	}
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return nil, fmt.Errorf("google doc id is required")
	}
	document, err := d.docsService.Documents.Get(docID).
		IncludeTabsContent(true).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve google doc: %w", err)
	}
	return document, nil
}

// UpdateDoc updates the title and content of an existing Google Doc.
// HTML content is rebuilt structurally so the title heading and code
// blocks survive; plain-text content is inserted as-is.
func (d *DocClientImpl) UpdateDoc(ctx context.Context, docID, title, content string) error {
	if d.driveService == nil {
		return fmt.Errorf("drive service not initialized")
	}
	if d.docsService == nil {
		return fmt.Errorf("google docs service not initialized")
	}

	if docTitle := strings.TrimSpace(title); docTitle != "" {
		if _, err := d.driveService.Files.Update(docID, &drive.File{Name: docTitle}).
			Fields("id").
			Context(ctx).
			Do(); err != nil {
			return fmt.Errorf("failed to rename google doc: %w", err)
		}
	}

	if err := d.clearDocumentBody(ctx, docID); err != nil {
		return err
	}

	if isHTMLContent(content) {
		return d.insertHTMLContent(ctx, docID, content)
	}
	return d.insertContent(ctx, docID, content)
}

// isHTMLContent reports whether content is a canonical document HTML
// payload rather than plain text.
func isHTMLContent(content string) bool {
	return strings.Contains(content, "<html") ||
		strings.Contains(content, "<div") ||
		strings.Contains(content, "<h1") ||
		strings.Contains(content, "<p>") ||
		strings.Contains(content, "<style")
}
