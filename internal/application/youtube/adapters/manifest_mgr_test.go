package adapters

import (
	"context"
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

type recordingAssetResolver struct {
	lastReq *asset.ResolveRequest
	result  *asset.ResolveResult
}

func (r *recordingAssetResolver) Resolve(_ context.Context, req *asset.ResolveRequest) (*asset.ResolveResult, error) {
	r.lastReq = req
	if r.result != nil {
		return r.result, nil
	}
	return &asset.ResolveResult{
		LocationKind: "drive",
		URI:          "resolved-folder-id",
		FolderID:     "resolved-folder-id",
		FolderPath:   "youtube/Pacquiao-Vs-Broner/Press-Conference",
	}, nil
}

func TestResolveDriveDestination_PayloadCreatesSubfolder(t *testing.T) {
	resolver := &recordingAssetResolver{}
	svc := &Service{
		log:               zap.NewNop(),
		assetDestResolver: resolver,
	}

	req := &youtubetypes.ExtractRequest{
		Destination: &youtubetypes.DestinationRequest{
			Group:           "Manny Pacquaio vs Adrien Broner Press Conference",
			FolderID:        "drive-root-123",
			FolderPath:      "youtube/Pacquiao-Vs-Broner",
			SubfolderName:   "yt_Press-Conference",
			CreateSubfolder: false,
		},
	}

	folderID, folderPath := svc.resolveDriveDestination(context.Background(), req, "vdC5GXxS-qU")

	if resolver.lastReq == nil {
		t.Fatal("expected resolver to be called")
	}
	if resolver.lastReq.Group != "Manny Pacquaio vs Adrien Broner Press Conference" {
		t.Fatalf("Group = %q, want payload group", resolver.lastReq.Group)
	}
	if resolver.lastReq.FolderID != "drive-root-123" {
		t.Fatalf("FolderID = %q, want payload folder_id", resolver.lastReq.FolderID)
	}
	if resolver.lastReq.FolderPath != "youtube/Pacquiao-Vs-Broner" {
		t.Fatalf("FolderPath = %q, want payload folder_path", resolver.lastReq.FolderPath)
	}
	if resolver.lastReq.SubfolderName != "Press-Conference" {
		t.Fatalf("SubfolderName = %q, want trimmed subfolder name", resolver.lastReq.SubfolderName)
	}
	if !resolver.lastReq.CreateSubfolder {
		t.Fatal("expected CreateSubfolder to be forced true")
	}
	if folderID != "resolved-folder-id" {
		t.Fatalf("folderID = %q, want resolved-folder-id", folderID)
	}
	if folderPath != "youtube/Pacquiao-Vs-Broner/Press-Conference" {
		t.Fatalf("folderPath = %q, want resolver folder path", folderPath)
	}
}
