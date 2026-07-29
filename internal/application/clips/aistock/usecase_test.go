package aistock

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Test doubles ───────────────────────────────────────────────────────

type fakeDriveReader struct {
	meta        *DriveFileMeta
	body        io.ReadCloser
	ct          string
	metaErr     error
	downloadErr error
}

func (f *fakeDriveReader) GetFileMeta(_ context.Context, _ string) (*DriveFileMeta, error) {
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.meta, nil
}

func (f *fakeDriveReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	if f.downloadErr != nil {
		return nil, "", f.downloadErr
	}
	return f.body, f.ct, nil
}

type fakeArtifactService struct {
	ref   *appupload.ArtifactRef
	lp    string
	err   error
	input *appupload.ArtifactCreateInput
}

func (f *fakeArtifactService) CreateAndVerify(_ context.Context, in appupload.ArtifactCreateInput) (*appupload.ArtifactRef, error) {
	f.input = &in
	if f.err != nil {
		return nil, f.err
	}
	return f.ref, nil
}

func (f *fakeArtifactService) LocalPath(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.lp, nil
}

type fakeDispatcher struct {
	clip *asset.Asset
	hash string
	err  error
}

func (f *fakeDispatcher) EnqueueAndIndex(_ context.Context, clip *asset.Asset, hash string) error {
	f.clip = clip
	f.hash = hash
	return f.err
}

// ── Helpers ─────────────────────────────────────────────────────────────

func validDocumentJSON() string {
	return `{
		"schema_version": "ai_stock_visual_analysis.v1",
		"asset": {
			"proposed_asset_id": "underwater-sand-jumpscare-01",
			"source": "ai_generated",
			"asset_role": "stock",
			"media_type": "video",
			"folder_path": "Stock/AI/Ocean/MarineLife",
			"normalized_group": "stock",
			"title": "Pesce predatore nascosto sotto la sabbia",
			"duration_ms": 7000,
			"width": 1080,
			"height": 1920,
			"fps": 30,
			"has_audio": true,
			"has_dialogue": false,
			"audio_profile": "ambient_and_effects"
		},
		"visual_analysis": {
			"summary_en": "A hand brushes away ocean sand underwater...",
			"summary_it": "Una mano sposta la sabbia...",
			"subjects": ["hand", "sand"],
			"environment": ["underwater"],
			"actions": ["brushing sand"]
		},
		"search_text": "Pesce predatore nascosto sotto la sabbia",
		"timed_events": [
			{
				"start_ms": 0,
				"end_ms": 2000,
				"event_it": "Una mano...",
				"event_en": "A hand..."
			}
		],
		"sound_cues": [],
		"recommended_clips": []
	}`
}

func newUseCase(t *testing.T, drive DriveReaderPort, artifact appupload.ArtifactServicePort, dispatcher *fakeDispatcher) *UseCase {
	t.Helper()
	uc, err := NewUseCase(UseCaseDeps{
		DriveReader: drive,
		Artifact:    artifact,
		Dispatcher:  dispatcher,
		Log:         zap.NewNop(),
	})
	require.NoError(t, err)
	return uc
}

// ── Tests ─────────────────────────────────────────────────────────────

func TestExecute_HappyPath(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{}

	uc := newUseCase(t, drive, artifact, dispatcher)
	res, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})

	require.NoError(t, err)
	assert.Equal(t, "underwater-sand-jumpscare-01", res.ClipID)
	assert.Equal(t, "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ", res.DriveFileID)
	assert.Equal(t, "/tmp/art-1.mp4", res.LocalPath)

	require.NotNil(t, dispatcher.clip)
	assert.Equal(t, "underwater-sand-jumpscare-01", dispatcher.clip.ID)
	assert.Equal(t, asset.SourceAIGenerated, dispatcher.clip.Source)
	assert.Equal(t, "Pesce predatore nascosto sotto la sabbia", dispatcher.clip.SearchText)
	assert.Equal(t, "sha256-abc", dispatcher.hash)
	require.NotNil(t, artifact.input)
	assert.Equal(t, "video", artifact.input.Kind)
	assert.Equal(t, "underwater-sand-jumpscare-01", artifact.input.ID)
	assert.Equal(t, "video/mp4", artifact.input.MimeType)
}

func TestExecute_InvalidDocument(t *testing.T) {
	uc := newUseCase(t, &fakeDriveReader{}, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: `{"schema_version": "ai_stock_visual_analysis.v1"}`,
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse document")
}

func TestExecute_EmptyDocument(t *testing.T) {
	uc := newUseCase(t, &fakeDriveReader{}, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: "",
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "document is required")
}

func TestExecute_EmptyDriveURL(t *testing.T) {
	uc := newUseCase(t, &fakeDriveReader{}, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drive_url is required")
}

func TestExecute_EmptyProposedAssetID(t *testing.T) {
	doc := validDocumentJSON()
	doc = strings.Replace(doc, `"proposed_asset_id": "underwater-sand-jumpscare-01"`, `"proposed_asset_id": ""`, 1)
	uc := newUseCase(t, &fakeDriveReader{}, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: doc,
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proposed_asset_id is required")
}

func TestExecute_InvalidDriveURL(t *testing.T) {
	uc := newUseCase(t, &fakeDriveReader{}, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://example.com/file/d/123/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid drive reference")
}

func TestExecute_GetFileMetaFailure(t *testing.T) {
	drive := &fakeDriveReader{
		meta:    &DriveFileMeta{Name: "underwater.mp4"},
		metaErr: assert.AnError,
	}
	uc := newUseCase(t, drive, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get drive file meta")
}

func TestExecute_DownloadFailure(t *testing.T) {
	drive := &fakeDriveReader{
		meta:        &DriveFileMeta{Name: "underwater.mp4"},
		downloadErr: assert.AnError,
	}
	uc := newUseCase(t, drive, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "download drive file")
}

func TestExecute_DispatcherFailure(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{err: assert.AnError}

	uc := newUseCase(t, drive, artifact, dispatcher)
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enqueue and index")
}

func TestExecute_AcceptsVariousDriveURLShapes(t *testing.T) {
	urls := []string{
		"https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
		"https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/edit",
		"https://drive.google.com/uc?id=1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ",
		"https://drive.google.com/open?id=1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ",
		"1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ",
	}
	for _, driveURL := range urls {
		drive := &fakeDriveReader{
			meta: &DriveFileMeta{Name: "underwater.mp4"},
			body: io.NopCloser(strings.NewReader("fake video bytes")),
			ct:   "video/mp4",
		}
		artifact := &fakeArtifactService{
			ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
			lp:  "/tmp/art-1.mp4",
		}
		dispatcher := &fakeDispatcher{}

		uc := newUseCase(t, drive, artifact, dispatcher)
		res, err := uc.Execute(context.Background(), CreateAIStockCommand{
			DocumentJSON: validDocumentJSON(),
			DriveURL:     driveURL,
		})
		require.NoError(t, err)
		assert.Equal(t, "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ", res.DriveFileID)
	}
}

func TestExecute_FallsBackContentTypeWhenEmpty(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{}

	uc := newUseCase(t, drive, artifact, dispatcher)
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.NoError(t, err)
	require.NotNil(t, artifact.input)
	assert.Equal(t, "video/mp4", artifact.input.MimeType)
}

func TestExecute_SetsCanonicalDriveLinks(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{}

	uc := newUseCase(t, drive, artifact, dispatcher)
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.NoError(t, err)
	require.NotNil(t, dispatcher.clip)
	assert.Equal(t, "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view", dispatcher.clip.DriveLink())
	assert.Equal(t, "https://drive.google.com/uc?export=download&id=1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ", dispatcher.clip.DownloadLink())
}

func TestExecute_RejectsNonVideoContentType(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.pdf"},
		body: io.NopCloser(strings.NewReader("fake pdf bytes")),
		ct:   "application/pdf",
	}
	uc := newUseCase(t, drive, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-video content type")
}

func TestExecute_AcceptsOctetStreamWithVideoExtension(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "application/octet-stream",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{}

	uc := newUseCase(t, drive, artifact, dispatcher)
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.NoError(t, err)
	require.NotNil(t, artifact.input)
	assert.Equal(t, "application/octet-stream", artifact.input.MimeType)
}

func TestExecute_RejectsOctetStreamWithoutVideoExtension(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.bin"},
		body: io.NopCloser(strings.NewReader("fake bytes")),
		ct:   "application/octet-stream",
	}
	uc := newUseCase(t, drive, &fakeArtifactService{}, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-video content type")
}

func TestExecute_RejectsEmptySHA256(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	uc := newUseCase(t, drive, artifact, &fakeDispatcher{})
	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: validDocumentJSON(),
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SHA256 is empty")
}

func TestExecute_FallsBackToComposedSearchText(t *testing.T) {
	drive := &fakeDriveReader{
		meta: &DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &fakeArtifactService{
		ref: &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234},
		lp:  "/tmp/art-1.mp4",
	}
	dispatcher := &fakeDispatcher{}

	uc := newUseCase(t, drive, artifact, dispatcher)
	doc := validDocumentJSON()
	doc = strings.Replace(doc, `"search_text": "Pesce predatore nascosto sotto la sabbia",`, `"search_text": "",`, 1)

	_, err := uc.Execute(context.Background(), CreateAIStockCommand{
		DocumentJSON: doc,
		DriveURL:     "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	require.NoError(t, err)
	require.NotNil(t, dispatcher.clip)
	assert.Contains(t, dispatcher.clip.SearchText, "Pesce predatore nascosto sotto la sabbia")
	assert.Contains(t, dispatcher.clip.SearchText, "A hand brushes away ocean sand underwater")
}
