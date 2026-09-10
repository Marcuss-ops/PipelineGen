package renderinggen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// DriveOverlayArtifactPublisher closes the queue→Drive boundary. RenderingGen
// keeps the certified bytes in its content-addressed object store; this
// adapter streams those bytes to a short-lived local file solely because the
// canonical Drive publisher accepts verified local artifacts.
type DriveOverlayArtifactPublisher struct {
	publisher finalization.PublisherPort
	client    *http.Client
}

func NewDriveOverlayArtifactPublisher(pub finalization.PublisherPort) *DriveOverlayArtifactPublisher {
	return &DriveOverlayArtifactPublisher{publisher: pub, client: objectStoreHTTPClient}
}

func (p *DriveOverlayArtifactPublisher) PublishOverlay(ctx context.Context, spec scriptgen.OverlayPublicationSpec, artifact *scriptgen.RenderArtifact) error {
	if p == nil || p.publisher == nil {
		return fmt.Errorf("overlay Drive publisher is not configured")
	}
	if artifact == nil || strings.TrimSpace(artifact.URL) == "" || strings.TrimSpace(artifact.SHA256) == "" || artifact.SizeBytes <= 0 {
		return fmt.Errorf("overlay artifact certification is incomplete")
	}

	ext := filepath.Ext(artifact.URL)
	if ext == "" {
		ext = ".mp4"
	}
	file, err := os.CreateTemp("", "pipelinegen-overlay-*"+ext)
	if err != nil {
		return fmt.Errorf("create overlay publication staging file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)

	if err := downloadCertifiedArtifact(ctx, p.client, artifact.URL, file, artifact.SizeBytes, artifact.SHA256); err != nil {
		_ = file.Close()
		return fmt.Errorf("stage certified overlay artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close overlay publication staging file: %w", err)
	}

	scriptName := strings.TrimSpace(spec.ScriptName)
	if scriptName == "" {
		scriptName = strings.TrimSpace(spec.ProjectID)
	}
	if scriptName == "" {
		return fmt.Errorf("overlay publication requires script name")
	}
	language := strings.TrimSpace(spec.Language)
	if language == "" {
		return fmt.Errorf("overlay publication requires language")
	}
	filename := fmt.Sprintf("%s-%s-overlay-%s.mp4",
		pathutil.SafeFolderName(scriptName),
		pathutil.SafeFolderName(language),
		strings.ToLower(artifact.SHA256[:minInt(len(artifact.SHA256), 12)]))
	artifactID := firstNonEmpty(spec.PlanID, artifact.ID, artifact.SHA256)
	loc, err := p.publisher.Publish(ctx, finalization.VerifiedArtifact{
		ArtifactID:       "overlay:" + artifactID,
		Kind:             finalization.KindVideo,
		Filename:         filename,
		LocalPath:        path,
		MIMEType:         firstNonEmpty(artifact.MimeType, "video/mp4"),
		SizeBytes:        artifact.SizeBytes,
		SHA256:           strings.ToLower(artifact.SHA256),
		SourceVersion:    1,
		Requirement:      finalization.ArtifactRequirementRequired,
		IdempotencyKey:   "overlay:" + artifact.SHA256,
		RootFolderName:   scriptName,
		Description:      "Chronon overlay " + scriptName + " (" + language + ")",
		Source:           "chronon",
		DriveSubpath:     []string{"overlay"},
		ProjectID:        scriptName,
		Language:         language,
		ArtifactMetadata: map[string]any{"script_name": scriptName, "language": language, "source": "chronon", "plan_id": spec.PlanID},
	})
	if err != nil {
		return err
	}
	artifact.DriveFileID = loc.FileID
	artifact.DriveLink = loc.WebViewLink
	return nil
}

func downloadCertifiedArtifact(ctx context.Context, client *http.Client, rawURL string, file *os.File, expectedSize int64, expectedSHA string) error {
	if client == nil {
		client = objectStoreHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	actual, err := digest.SHA256Reader(file)
	if err != nil {
		return err
	}
	if written != expectedSize {
		return fmt.Errorf("downloaded size %d, want %d", written, expectedSize)
	}
	if !strings.EqualFold(actual, expectedSHA) {
		return fmt.Errorf("downloaded SHA-256 %s, want %s", actual, expectedSHA)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ scriptgen.OverlayArtifactPublisher = (*DriveOverlayArtifactPublisher)(nil)
var _ finalization.PublisherPort = (*drive.ArtifactPublisherAdapter)(nil)
