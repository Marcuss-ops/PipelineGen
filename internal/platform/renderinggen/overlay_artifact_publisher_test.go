package renderinggen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type captureOverlayPublisher struct {
	artifacts []finalization.VerifiedArtifact
	payloads  [][]byte
}

func (p *captureOverlayPublisher) Publish(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	p.artifacts = append(p.artifacts, artifact)
	data, _ := os.ReadFile(artifact.LocalPath)
	p.payloads = append(p.payloads, data)
	return finalization.AssetLocation{Provider: "drive", FileID: "drive-file", WebViewLink: "https://drive/file", FolderID: "drive-folder"}, nil
}

// TestDownloadCertifiedArtifactHashesStreamedBytes pins the single-pass
// staging contract and its fail-closed verification.
func TestDownloadCertifiedArtifactHashesStreamedBytes(t *testing.T) {
	payload := []byte("certified overlay bytes")
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	url := srv.URL + "/objects/" + hash

	file, err := os.CreateTemp(t.TempDir(), "artifact-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer file.Close()
	if err := downloadCertifiedArtifact(context.Background(), srv.Client(), url, file, int64(len(payload)), hash); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("read staged artifact: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("staged bytes = %q, want %q", got, payload)
	}

	sizeFile, err := os.CreateTemp(t.TempDir(), "artifact-size-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer sizeFile.Close()
	if err := downloadCertifiedArtifact(context.Background(), srv.Client(), url, sizeFile, int64(len(payload)+1), hash); err == nil {
		t.Fatal("size drift must fail closed")
	}

	hashFile, err := os.CreateTemp(t.TempDir(), "artifact-hash-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer hashFile.Close()
	if err := downloadCertifiedArtifact(context.Background(), srv.Client(), url, hashFile, int64(len(payload)), strings.Repeat("0", 64)); err == nil {
		t.Fatal("hash drift must fail closed")
	}
}

func TestDriveOverlayArtifactPublisherPublishesVerifiedArtifactToConfiguredRoot(t *testing.T) {
	payload := []byte("certified overlay bytes")
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer store.Close()

	capture := &captureOverlayPublisher{}
	publisher := &DriveOverlayArtifactPublisher{publisher: capture, client: store.Client()}
	publisher.SetRootFolderID("1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E")
	artifact := &scriptgen.RenderArtifact{ID: "render-1", URL: store.URL + "/objects/" + hash, SHA256: hash, SizeBytes: int64(len(payload)), MimeType: "video/mp4"}
	err := publisher.PublishOverlay(context.Background(), scriptgen.OverlayPublicationSpec{
		ScriptName: "Donald Trump", Language: "it", ProjectID: "Donald Trump", PlanID: "plan-1",
		CompletionWait: 1500 * time.Millisecond, PollingSleep: 250 * time.Millisecond,
		PollingInterval: 100 * time.Millisecond, PollCount: 15,
	}, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.DriveFileID != "drive-file" || artifact.DriveLink == "" {
		t.Fatalf("drive reference = %#v, want populated reference", artifact)
	}
	if artifact.DriveFolderID != "drive-folder" {
		t.Fatalf("drive folder = %q, want drive-folder", artifact.DriveFolderID)
	}
	if len(capture.artifacts) != 2 {
		t.Fatalf("publication count = %d, want video + receipt", len(capture.artifacts))
	}
	video := capture.artifacts[0]
	if video.Source != "chronon" || video.ProjectID != "Donald Trump" || video.Language != "it" {
		t.Fatalf("publication metadata = %#v", video)
	}
	if video.ResolvedFolderID != "1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E" || strings.Join(video.DriveSubpath, "/") != finalization.OverlayChildFolder {
		t.Fatalf("configured Drive parent/overlay path was not pinned: %#v", video)
	}
	if video.Filename == "" || video.LocalPath == "" {
		t.Fatalf("publication artifact missing filename/local path: %#v", video)
	}
	if _, err := os.Stat(video.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("staging file should be removed after publication, stat error=%v", err)
	}
	receipt := capture.artifacts[1]
	if receipt.Kind != finalization.KindScript || receipt.Source != "chronon_receipt" || receipt.MIMEType != "application/json" {
		t.Fatalf("receipt artifact metadata = %#v", receipt)
	}
	if strings.Join(receipt.DriveSubpath, "/") != finalization.OverlayChildFolder || receipt.ResolvedFolderID != video.ResolvedFolderID {
		t.Fatalf("receipt path = %#v, want same parent/overlay path", receipt)
	}
	if receipt.SizeBytes <= 0 || receipt.SHA256 == "" {
		t.Fatalf("receipt is not certified: %#v", receipt)
	}
	var decoded overlayTimingReceipt
	if err := json.Unmarshal(capture.payloads[1], &decoded); err != nil {
		t.Fatalf("receipt JSON: %v", err)
	}
	if decoded.Kind != "chronon_overlay_receipt" || decoded.Video.DriveFileID != "drive-file" || decoded.Timing.CompletionWaitMS != 1500 || decoded.Timing.PollingSleepMS != 250 || decoded.Timing.PollCount != 15 {
		t.Fatalf("receipt contents = %#v", decoded)
	}
}

func TestDriveOverlayArtifactPublisherRequiresConfiguredRoot(t *testing.T) {
	publisher := &DriveOverlayArtifactPublisher{publisher: &captureOverlayPublisher{}}
	artifact := &scriptgen.RenderArtifact{URL: "https://store.invalid/overlay.mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1}
	if err := publisher.PublishOverlay(context.Background(), scriptgen.OverlayPublicationSpec{ScriptName: "test", Language: "en"}, artifact); err == nil || !strings.Contains(err.Error(), "configured root folder") {
		t.Fatalf("missing root error = %v, want configured-root failure", err)
	}
}

func TestDriveOverlayArtifactPublisherPinsConfiguredRootFolder(t *testing.T) {
	payload := []byte("certified overlay bytes")
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer store.Close()

	capture := &captureOverlayPublisher{}
	publisher := &DriveOverlayArtifactPublisher{publisher: capture, client: store.Client()}
	publisher.SetRootFolderID("1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E")
	artifact := &scriptgen.RenderArtifact{ID: "render-root", URL: store.URL + "/objects/" + hash, SHA256: hash, SizeBytes: int64(len(payload)), MimeType: "video/mp4"}
	if err := publisher.PublishOverlay(context.Background(), scriptgen.OverlayPublicationSpec{
		ScriptName: "Ada Lovelace", Language: "it", PlanID: "plan-root",
	}, artifact); err != nil {
		t.Fatal(err)
	}
	if len(capture.artifacts) != 2 {
		t.Fatalf("publication count = %d, want video + receipt", len(capture.artifacts))
	}
	if capture.artifacts[0].ResolvedFolderID != "1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E" || !capture.artifacts[0].RootFolderResolved {
		t.Fatalf("configured Drive parent was not pinned: %#v", capture.artifacts[0])
	}
	if strings.Join(capture.artifacts[0].DriveSubpath, "/") != finalization.OverlayChildFolder {
		t.Fatalf("overlay child path was not requested: %#v", capture.artifacts[0])
	}
}

func TestDriveOverlayArtifactPublisherUsesJobSelectedRootBeforeConfiguredRoot(t *testing.T) {
	payload := []byte("job-routed overlay bytes")
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer store.Close()

	capture := &captureOverlayPublisher{}
	publisher := &DriveOverlayArtifactPublisher{publisher: capture, client: store.Client()}
	publisher.SetRootFolderID("configured-root")
	artifact := &scriptgen.RenderArtifact{
		ID: "render-job-root", URL: store.URL + "/objects/" + hash,
		SHA256: hash, SizeBytes: int64(len(payload)), MimeType: "video/mp4",
	}
	if err := publisher.PublishOverlay(context.Background(), scriptgen.OverlayPublicationSpec{
		ScriptName: "Michael Jordan", Language: "en", PlanID: "plan-job-root",
		DriveFolderID: "job-selected-root",
	}, artifact); err != nil {
		t.Fatal(err)
	}
	if len(capture.artifacts) != 2 {
		t.Fatalf("publication count = %d, want video + receipt", len(capture.artifacts))
	}
	for i, published := range capture.artifacts {
		if published.ResolvedFolderID != "job-selected-root" {
			t.Fatalf("publication %d used folder %q, want job-selected-root: %#v", i, published.ResolvedFolderID, published)
		}
		if strings.Join(published.DriveSubpath, "/") != finalization.OverlayChildFolder {
			t.Fatalf("publication %d path = %#v, want overlay child", i, published.DriveSubpath)
		}
	}
}
