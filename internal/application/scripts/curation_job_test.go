package scripts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

func TestCurationJobServiceImpl_UsesScriptsGenFolderForGoogleDoc(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Drive: config.DriveConfig{
			ScriptsRootFolder:     "scripts-root",
			ScriptsGenerateFolder: "scripts-gen-folder",
		},
	}

	var capturedFolderID string
	svc := NewCurationJobServiceImpl(
		&MediaCurator{},
		nil,
		cfg,
		zap.NewNop(),
		nil,
		nil,
		func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
			capturedFolderID = folderID
			return "https://docs.example.com/doc-123", "doc-123"
		},
	)

	payload := JobPayloadCurate{
		Title: "My Script",
		Query: "Why observability matters",
	}
	rawPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	j := &job.Job{
		ID:      "job-1",
		Type:    job.TypeMediaCurate,
		Payload: rawPayload,
	}

	result, err := svc.HandleCurateJob(context.Background(), j, &appjobs.JobTools{})
	require.NoError(t, err)
	require.Equal(t, "scripts-gen-folder", capturedFolderID)
	require.Equal(t, "https://docs.example.com/doc-123", result["doc_link"])
	require.Equal(t, "doc-123", result["doc_id"])
}
