package scripts

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type pipelineDocumentClient struct {
	content string
	folderID string
}

func (c *pipelineDocumentClient) CreateDoc(_ context.Context, _, content, folderID string) (*drive.Doc, error) {
	c.content = content
	c.folderID = folderID
	return &drive.Doc{URL: "https://docs.google.com/document/d/doc-1", ID: "doc-1"}, nil
}
func (c *pipelineDocumentClient) ShareDoc(context.Context, string, string, string) error { return nil }
func (c *pipelineDocumentClient) ListRecentDocs(context.Context, string, int) ([]drive.Doc, error) { return nil, nil }
func (c *pipelineDocumentClient) UpdateDoc(context.Context, string, string, string) error { return nil }

func TestPipelineRunCreatesDocWithClipMap(t *testing.T) {
	client := &pipelineDocumentClient{}
	docs := NewDocumentsService(client, zap.NewNop(), "default-script-folder")
	pipeline := NewPipeline(zap.NewNop(), "", nil, docs, nil, nil)
	result, err := pipeline.RunWithClipScenes(
		context.Background(),
		&scriptpkg.GenerationSpec{Title: "Clip Story", CreateDoc: true, DriveFolderID: "script-folder"},
		"Generated narrative.",
		[]ClipScene{{SceneIndex: 0, ClipID: "clip-1", Kind: "clip", Text: "Generated narrative."}},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "doc-1", result.DocID)
	require.Empty(t, result.DocError)
	require.Equal(t, "script-folder", client.folderID)
	require.Contains(t, client.content, "clip_id: clip-1")
	require.Contains(t, client.content, "Generated narrative.")
}
