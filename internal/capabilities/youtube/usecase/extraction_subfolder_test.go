// Package usecase — extraction_subfolder_test.go pins the Drive-subfolder
// regression guard: when a caller requests CreateSubfolder + SubfolderName
// under an explicit root FolderID, the extraction MUST materialise the
// CHILD folder (get-or-create) and upload into it — never the root — and
// folder_path must match the real Drive parent (the subfolder, not the
// root). This closes the silent-wrong-location bug where the subfolder was
// reduced to a metadata string and the clips landed in the root.
package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	assetdomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeAssetDestResolver records the asset.Resolver call and returns a
// canned result. It is the canonical seam for asserting that the
// extraction pipeline asked for a child folder under the explicit root.
type fakeAssetDestResolver struct {
	lastReq *assetdomain.ResolveRequest
	result  *assetdomain.ResolveResult
	err     error
}

func (f *fakeAssetDestResolver) Resolve(_ context.Context, req *assetdomain.ResolveRequest) (*assetdomain.ResolveResult, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

var _ assetdomain.Resolver = (*fakeAssetDestResolver)(nil)

// TestExtract_CreateSubfolder_ResolvesChildFolderNotRoot pins the
// canonical path: Extract must route the subfolder request through the
// asset.Resolver, upload into the CHILD folder, and keep folder_path
// matching the subfolder (the real Drive parent of the clips).
func TestExtract_CreateSubfolder_ResolvesChildFolderNotRoot(t *testing.T) {
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	resolver := &fakeAssetDestResolver{
		result: &assetdomain.ResolveResult{
			LocationKind: "drive",
			FolderID:     "child-folder-id",
			FolderPath:   "Di-Awl0XyQs_Celebrity_Interviews",
		},
	}

	tmp := t.TempDir()
	pipeline := &fakeVideoPipeline{err: errYtDlpFailed}

	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Cfg: testConfig(tmp), Log: zap.NewNop()},
		ServiceAssetDeps{
			AssetDestResolver: resolver,
			MediaProcessor:    nil,
			LifecycleService:  nil,
		},
		ServiceVideoDeps{
			VideoPipeline: pipeline,
			ProcessSeg:    newTestProcessSegmentUseCase(zap.NewNop(), pipeline),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{},
	)

	resp, err := svc.Extract(context.Background(), &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=abc123",
		Segments: []youtubetypes.Segment{
			{Name: "clip", Start: "0", End: "10"},
		},
		Destination: &youtubetypes.DestinationRequest{
			FolderID:        "root-folder-id",
			FolderPath:      "Di-Awl0XyQs_Celebrity_Interviews",
			SubfolderName:   "Di-Awl0XyQs_Celebrity_Interviews",
			CreateSubfolder: true,
		},
	})
	require.NoError(t, err, "Extract must not error on the subfolder resolution path")
	require.NotNil(t, resp)

	// The resolver must be asked to create the subfolder under the ROOT.
	require.NotNil(t, resolver.lastReq, "Extract must consult the asset.Resolver for a requested subfolder")
	require.Equal(t, "root-folder-id", resolver.lastReq.FolderID, "the parent must be the explicit root, not a pre-resolved child")
	require.Equal(t, "Di-Awl0XyQs_Celebrity_Interviews", resolver.lastReq.SubfolderName)
	require.True(t, resolver.lastReq.CreateSubfolder)

	// The extraction must target the CHILD folder (not the root), and the
	// folder_path must match the subfolder — the real Drive parent of the
	// uploaded clips.
	require.Equal(t, "child-folder-id", resp.DriveFolderID,
		"clips must upload into the materialised child folder, not the root")
	require.Equal(t, "Di-Awl0XyQs_Celebrity_Interviews", resp.DriveFolderPath,
		"folder_path must match the real Drive parent (the subfolder)")
}

// TestBuildSegmentCommand_CarriesResolvedChildFolder pins the Step 8 handoff:
// the resolved child folder id + path must be threaded into the
// ProcessSegmentCommand the per-segment pipeline (Step 8 upload) consumes.
func TestBuildSegmentCommand_CarriesResolvedChildFolder(t *testing.T) {
	req := &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=abc123",
		Destination: &youtubetypes.DestinationRequest{
			FolderID:      "root-folder-id",
			SubfolderName: "Di-Awl0XyQs_Celebrity_Interviews",
		},
	}
	seg := youtubetypes.Segment{Name: "clip", Start: "0", End: "10"}

	cmd := buildSegmentCommand(req, seg, 0, "abc123", "/tmp/out",
		"child-folder-id", "Di-Awl0XyQs_Celebrity_Interviews", true)

	require.Equal(t, "child-folder-id", cmd.DriveFolderID,
		"Step 8 must receive the CHILD folder id, not the root")
	require.Equal(t, "Di-Awl0XyQs_Celebrity_Interviews", cmd.DriveFolderPath,
		"Step 8 must receive the subfolder path (the real Drive parent)")
}

// recordingDriveFolderMgr records the folder id passed to the Step 8 upload
// so the test can assert the clips land in the CHILD folder, not the root.
type recordingDriveFolderMgr struct {
	called         bool
	uploadFolderID string
	uploadPath     string
	uploadFilename string
}

func (r *recordingDriveFolderMgr) GetOrCreateFolder(_ context.Context, _ string, parentFolderID string) (string, error) {
	return parentFolderID, nil
}

func (r *recordingDriveFolderMgr) UploadFileIfChanged(_ context.Context, localPath, folderID, filename, _, _ string) (*youtubeports.UploadResultDTO, bool, error) {
	r.called = true
	r.uploadFolderID = folderID
	r.uploadPath = localPath
	r.uploadFilename = filename
	return &youtubeports.UploadResultDTO{
		FileID:      "drive-file-01",
		WebViewLink: "https://drive.google.com/file/d/drive-file-01/view",
	}, false, nil
}

// Compile-time guard: recordingDriveFolderMgr satisfies
// youtubeports.DriveFolderManagerPort.
var _ youtubeports.DriveFolderManagerPort = (*recordingDriveFolderMgr)(nil)

// TestStep8_UploadsIntoResolvedChildFolderNotRoot pins the per-segment
// Step 8 seam: the DriveFolderMgr upload MUST target the resolved CHILD
// folder (cmd.DriveFolderID), never the root, and the committed ClipAsset
// MUST carry folder_path = the subfolder (the real Drive parent of the
// uploaded clips). This closes the gap left by the Extract-level and
// command-handoff tests, which stop before the actual Drive upload.
func TestStep8_UploadsIntoResolvedChildFolderNotRoot(t *testing.T) {
	rec := &recordingDriveFolderMgr{}
	writer := &stubWriterAssetRecorder{}

	core, media, metadata, observability := validProcessSegmentDeps()
	media.DriveFolderMgr = rec
	core.Writer = writer

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID:         "abc123",
		DriveFolderID:   "child-folder-id", // the materialised child, NOT root
		DriveFolderPath: "Di-Awl0XyQs_Celebrity_Interviews",
		Segment:         youtubetypes.Segment{Name: "clip 01", Start: "0", End: "85"},
	}
	out := youtubetypes.ProcessSegmentResult{
		Item: youtubetypes.ExtractItem{Filename: "clip-01.mp4"},
	}

	_, err := uc.step6to9_SubtitlesDriveWriter(
		context.Background(), cmd, &out, "yt_abc123_0_85_v1", 0, 85,
		"/tmp/clip-01.mp4", "deadbeef", "v1",
	)
	require.NoError(t, err, "Step 6-9 must complete with a wired DriveFolderMgr + writer")

	// (a) Step 8 upload MUST target the CHILD folder, not the root.
	require.True(t, rec.called, "Step 8 must invoke UploadFileIfChanged")
	require.Equal(t, "child-folder-id", rec.uploadFolderID,
		"Step 8 must upload into the resolved child folder, not the root")
	require.Equal(t, "clip-01.mp4", rec.uploadFilename)

	// (b) The uploaded Drive identity must be threaded onto the result.
	require.Equal(t, "drive-file-01", out.Item.DriveFileID)
	require.Equal(t, "https://drive.google.com/file/d/drive-file-01/view", out.Item.DriveLink)

	// (c) The committed ClipAsset must record folder_path = the real
	//     Drive parent (the subfolder), consistent with where the clip
	//     actually landed.
	require.Equal(t, 1, writer.calls, "Step 9 must commit exactly one ClipAsset")
	require.Equal(t, "child-folder-id", writer.captured.Drive.FolderID,
		"ClipAsset.Drive.FolderID must be the child folder")
	require.Equal(t, "Di-Awl0XyQs_Celebrity_Interviews", writer.captured.Drive.FolderPath,
		"ClipAsset.Drive.FolderPath must match the real Drive parent (the subfolder)")
}

// errYtDlpFailed forces the per-segment pipeline to fail-fast so the test
// exercises only the destination-resolution seam (the video-cut error is
// irrelevant to the subfolder assertion).
var errYtDlpFailed = errString("yt-dlp failed")

type errString string

func (e errString) Error() string { return string(e) }

// Compile-time guard: fakeVideoPipeline (service_test.go) satisfies
// youtubeports.VideoPipelinePort — kept here for reader clarity.
var _ youtubeports.VideoPipelinePort = (*fakeVideoPipeline)(nil)
