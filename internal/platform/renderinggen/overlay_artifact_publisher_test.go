package renderinggen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type captureOverlayPublisher struct {
	artifact finalization.VerifiedArtifact
}

func (p *captureOverlayPublisher) Publish(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	p.artifact = artifact
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
	if capture.artifact.Source != "chronon" || capture.artifact.ProjectID != "Donald Trump" || capture.artifact.Language != "it" {
		t.Fatalf("publication metadata = %#v", capture.artifact)
	}
	if capture.artifact.ResolvedFolderID != "1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E" || !capture.artifact.DirectDriveRoot {
		t.Fatalf("configured Drive root was not pinned: %#v", capture.artifact)
	}
	if len(capture.artifact.DriveSubpath) != 0 {
		t.Fatalf("configured root must receive the render directly, got subpath %#v", capture.artifact.DriveSubpath)
	}
	if capture.artifact.Filename == "" || capture.artifact.LocalPath == "" {
		t.Fatalf("publication artifact missing filename/local path: %#v", capture.artifact)
	}
	if _, err := os.Stat(capture.artifact.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("staging file should be removed after publication, stat error=%v", err)
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
	if capture.artifact.ResolvedFolderID != "1eRYRBDBWxGdqC4u7fHwp5hX_kRoTkZ8E" || !capture.artifact.RootFolderResolved {
		t.Fatalf("configured Drive root was not pinned: %#v", capture.artifact)
	}
	if len(capture.artifact.DriveSubpath) != 0 {
		t.Fatalf("configured root must receive the render directly, got subpath %#v", capture.artifact.DriveSubpath)
	}
	if !capture.artifact.DirectDriveRoot {
		t.Fatal("configured root must mark the artifact as direct-to-root")
	}
}
