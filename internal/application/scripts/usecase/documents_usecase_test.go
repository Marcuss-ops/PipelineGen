package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

type fakeDocClient struct {
	createDocErr error
	lastTitle    string
	calls        int
}

func (f *fakeDocClient) CreateDoc(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
	f.calls++
	f.lastTitle = title
	if f.createDocErr != nil {
		return nil, f.createDocErr
	}
	return &drive.Doc{ID: "doc-123", URL: "https://docs.example.com/doc-123"}, nil
}

func (f *fakeDocClient) ShareDoc(ctx context.Context, docID, email, role string) error { return nil }
func (f *fakeDocClient) ListRecentDocs(ctx context.Context, folderID string, limit int) ([]drive.Doc, error) {
	return nil, nil
}
func (f *fakeDocClient) UpdateDoc(ctx context.Context, docID, title, content string) error {
	return nil
}

func TestDocumentsUseCase_NilUseCase(t *testing.T) {
	t.Parallel()
	var uc *DocumentsUseCase
	ln, docID, err := uc.BuildAndCreate(context.Background(), "T", "C", nil, "F")
	require.ErrorIs(t, err, ErrDocumentCreationFailed)
	assert.Empty(t, ln)
	assert.Empty(t, docID)
}

func TestDocumentsUseCase_NilDocClient(t *testing.T) {
	t.Parallel()
	uc := NewDocumentsUseCase(nil, zap.NewNop(), "")
	assert.Nil(t, uc.DocumentsService())
	ln, docID, err := uc.BuildAndCreate(context.Background(), "T", "C", nil, "F")
	require.ErrorIs(t, err, ErrDocumentCreationFailed)
	assert.Empty(t, ln)
	assert.Empty(t, docID)
}

func TestDocumentsUseCase_ValidFolderID(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root-folder")

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		assert.Equal(t, "my-folder-id", input)
		return "my-folder-id", nil
	}

	ln, docID, err := uc.BuildAndCreate(context.Background(), "My Title", "content", resolveFolder, "my-folder-id")
	require.NoError(t, err)
	assert.NotEmpty(t, ln)
	assert.NotEmpty(t, docID)
	assert.Equal(t, "My Title", fc.lastTitle)
}

func TestDocumentsUseCase_ResolverError(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return "", errors.New("folder not found")
	}

	ln, docID, err := uc.BuildAndCreate(context.Background(), "T", "content", resolveFolder, "bad-folder")
	require.NoError(t, err)
	assert.NotEmpty(t, ln)
	assert.NotEmpty(t, docID)
}

func TestDocumentsUseCase_DocClientError(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{createDocErr: errors.New("drive unavailable")}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil
	}

	ln, docID, err := uc.BuildAndCreate(context.Background(), "T", "content", resolveFolder, "f")
	require.ErrorIs(t, err, ErrDocumentCreationFailed)
	assert.Empty(t, ln)
	assert.Empty(t, docID)
}

func TestDocumentsUseCase_ContextCancelled(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil
	}

	ln, docID, err := uc.BuildAndCreate(ctx, "T", "content", resolveFolder, "f")
	if err != nil {
		assert.Empty(t, ln)
		assert.Empty(t, docID)
	}
	_ = docID
}

func TestDocumentsUseCase_EmptyTitle(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil
	}

	ln, docID, err := uc.BuildAndCreate(context.Background(), "", "content", resolveFolder, "f")
	require.NoError(t, err)
	assert.NotEmpty(t, ln, "empty title should still create doc with fallback title")
	_ = docID
}

func TestDocumentsUseCase_EmptyContent(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil
	}

	ln, docID, err := uc.BuildAndCreate(context.Background(), "Title", "", resolveFolder, "f")
	require.NoError(t, err)
	assert.NotEmpty(t, ln, "empty content should still create doc")
	_ = docID
}

func TestDocumentsUseCase_FolderResolverCalledOnce(t *testing.T) {
	t.Parallel()
	fc := &fakeDocClient{}
	uc := NewDocumentsUseCase(fc, zap.NewNop(), "root")

	callCount := 0
	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		callCount++
		return input, nil
	}

	_, _, err := uc.BuildAndCreate(context.Background(), "T", "content", resolveFolder, "f")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "FolderResolver must be called exactly once")
}
