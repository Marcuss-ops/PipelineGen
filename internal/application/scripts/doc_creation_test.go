package scripts

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// mockDocClient implements drive.DocClient for testing createBatchDoc.
type mockDocClient struct {
	createDocFn func(ctx context.Context, title, content, folderID string) (*drive.Doc, error)
}

func (m *mockDocClient) CreateDoc(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
	if m.createDocFn != nil {
		return m.createDocFn(ctx, title, content, folderID)
	}
	return &drive.Doc{URL: "https://docs.example.com/doc", ID: "doc-123"}, nil
}

func (m *mockDocClient) ShareDoc(ctx context.Context, docID, email, role string) error { return nil }
func (m *mockDocClient) ListRecentDocs(ctx context.Context, folderID string, limit int) ([]drive.Doc, error) {
	return nil, nil
}
func (m *mockDocClient) UpdateDoc(ctx context.Context, docID, title, content string) error { return nil }

// ── Document Creation Tests ────────────────────────────────────────────────

func TestCreateBatchDoc_NilDocClient_ReturnsEmptyStrings(t *testing.T) {
	svc := &BatchService{
		docClient: nil,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Chapter 1", content: "Some content."},
	}

	docURL, docID := svc.createBatchDoc(context.Background(), "Test Title", parts, false, "en", "folder-1")
	assert.Empty(t, docURL)
	assert.Empty(t, docID)
}

func TestCreateBatchDoc_DocClientError_ReturnsEmptyStrings(t *testing.T) {
	mock := &mockDocClient{
		createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
			return nil, errors.New("drive unavailable")
		},
	}
	svc := &BatchService{
		docClient: mock,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Chapter 1", content: "Content."},
	}

	docURL, docID := svc.createBatchDoc(context.Background(), "Test", parts, false, "en", "f1")
	assert.Empty(t, docURL)
	assert.Empty(t, docID)
}

func TestCreateBatchDoc_Success_ReturnsURLAndID(t *testing.T) {
	mock := &mockDocClient{
		createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
			return &drive.Doc{URL: "https://docs.example.com/my-doc", ID: "abc-456"}, nil
		},
	}
	svc := &BatchService{
		docClient: mock,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Chapter 1", content: "Chapter content."},
	}

	docURL, docID := svc.createBatchDoc(context.Background(), "My Doc", parts, false, "it", "folder-42")
	assert.Equal(t, "https://docs.example.com/my-doc", docURL)
	assert.Equal(t, "abc-456", docID)
}

func TestCreateBatchDoc_PassesCorrectTitleAndFolder(t *testing.T) {
	var capturedTitle, capturedFolder string
	mock := &mockDocClient{
		createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
			capturedTitle = title
			capturedFolder = folderID
			return &drive.Doc{URL: "u", ID: "i"}, nil
		},
	}
	svc := &BatchService{
		docClient: mock,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Ch1", content: "C"},
	}

	svc.createBatchDoc(context.Background(), "Expected Title", parts, false, "fr", "target-folder")
	assert.Equal(t, "Expected Title", capturedTitle)
	assert.Equal(t, "target-folder", capturedFolder)
}

func TestCreateBatchDoc_GeneratesHTMLWithChapterHeadings(t *testing.T) {
	var capturedContent string
	mock := &mockDocClient{
		createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
			capturedContent = content
			return &drive.Doc{URL: "u", ID: "i"}, nil
		},
	}
	svc := &BatchService{
		docClient: mock,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Introduction", content: "Welcome."},
		{topic: "Main", content: "Body."},
	}

	svc.createBatchDoc(context.Background(), "Book", parts, false, "en", "f1")
	assert.Contains(t, capturedContent, "<h1>Book</h1>")
	assert.Contains(t, capturedContent, "<h2>Chapter 1: Introduction</h2>")
	assert.Contains(t, capturedContent, "<h2>Chapter 2: Main</h2>")
	assert.NotContains(t, capturedContent, "##") // no raw markdown
}

func TestCreateBatchDoc_NoChapters_OmitsChapterHeadings(t *testing.T) {
	var capturedContent string
	mock := &mockDocClient{
		createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
			capturedContent = content
			return &drive.Doc{URL: "u", ID: "i"}, nil
		},
	}
	svc := &BatchService{
		docClient: mock,
		log:       zap.NewNop(),
	}

	parts := []GeneratedPart{
		{topic: "Flow", content: "Continuous narrative."},
	}

	svc.createBatchDoc(context.Background(), "No Chaps", parts, true, "en", "f1")
	assert.NotContains(t, capturedContent, "Table of Contents")
	assert.NotContains(t, capturedContent, "Chapter 1")
	assert.Contains(t, capturedContent, "Continuous narrative.")
}

func TestCreateBatchDoc_LocalisedChapterLabel(t *testing.T) {
	tests := []struct {
		lang     string
		expected string
	}{
		{"it", "Capitolo"},
		{"fr", "Chapitre"},
		{"es", "Capítulo"},
		{"de", "Kapitel"},
		{"en", "Chapter"},
		{"xx", "Chapter"}, // unknown → English fallback
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			var capturedContent string
			mock := &mockDocClient{
				createDocFn: func(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
					capturedContent = content
					return &drive.Doc{URL: "u", ID: "i"}, nil
				},
			}
			svc := &BatchService{
				docClient: mock,
				log:       zap.NewNop(),
			}

			parts := []GeneratedPart{
				{topic: "Topic", content: "Content."},
			}

			svc.createBatchDoc(context.Background(), "Title", parts, false, tc.lang, "f1")
			assert.Contains(t, capturedContent, tc.expected+" 1: Topic")
		})
	}
}
