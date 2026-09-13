package renderinggen

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// DriveOverlayArtifactPublisher closes the queue→Drive boundary. RenderingGen
// keeps the certified bytes in its content-addressed object store; this
// adapter streams those bytes to a short-lived local file solely because the
// canonical Drive publisher accepts verified local artifacts.
type DriveOverlayArtifactPublisher struct {
	publisher finalization.PublisherPort
	client    *http.Client
	// rootFolderID is the explicit Drive folder selected by the composition
	// root for generated overlay renders. Production requires it: uploads are
	// pinned to this folder by code after render certification.
	rootFolderID          string
	scriptLanguageRouting bool
}

func NewDriveOverlayArtifactPublisher(pub finalization.PublisherPort) *DriveOverlayArtifactPublisher {
	return &DriveOverlayArtifactPublisher{publisher: pub, client: objectStoreHTTPClient}
}

// SetRootFolderID pins generated overlay renders to one configured and
// startup-validated Drive folder, so the caller never has to choose a folder
// manually after rendering.
func (p *DriveOverlayArtifactPublisher) SetRootFolderID(folderID string) {
	if p != nil {
		p.rootFolderID = strings.TrimSpace(folderID)
	}
}

// SetScriptLanguageRouting places overlays below the same project/language
// tree used by generated Docs: <root>/<script>/<language>/overlay.
func (p *DriveOverlayArtifactPublisher) SetScriptLanguageRouting(on bool) {
	if p != nil {
		p.scriptLanguageRouting = on
	}
}

func (p *DriveOverlayArtifactPublisher) PublishOverlay(ctx context.Context, spec scriptgen.OverlayPublicationSpec, artifact *scriptgen.RenderArtifact) error {
	if p == nil || p.publisher == nil {
		return fmt.Errorf("overlay Drive publisher is not configured")
	}
	rootFolderID := firstNonEmpty(spec.DriveFolderID, p.rootFolderID)
	if rootFolderID == "" {
		return fmt.Errorf("overlay Drive publisher requires configured root folder or job drive folder")
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
		strings.ToLower(artifact.SHA256[:min(len(artifact.SHA256), 12)]))
	artifactID := firstNonEmpty(spec.PlanID, artifact.ID, artifact.SHA256)
	verified := finalization.VerifiedArtifact{
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
		ProjectID:        scriptName,
		Language:         language,
		ArtifactMetadata: map[string]any{"script_name": scriptName, "language": language, "source": "chronon", "plan_id": spec.PlanID},
	}
	// The configured root is the parent selected by the operator. The
	// canonical delivery publisher creates/reuses the deterministic `overlay`
	// child below it. The same path is used for the JSON timing receipt.
	verified.ResolvedFolderID = rootFolderID
	verified.RootFolderResolved = true
	verified.DriveSubpath = []string{finalization.OverlayChildFolder}
	if p.scriptLanguageRouting {
		verified.DriveSubpath = []string{scriptName, language, finalization.OverlayChildFolder}
	}
	verified.ArtifactMetadata["overlay_item_id"] = spec.OverlayItemID
	verified.ArtifactMetadata["overlay_item_kind"] = spec.OverlayItemKind
	verified.ArtifactMetadata["source_start_us"] = spec.SourceStartUS
	verified.ArtifactMetadata["source_end_us"] = spec.SourceEndUS
	verified.ArtifactMetadata["target_duration_us"] = spec.TargetDurationUS
	videoPublishStarted := time.Now()
	loc, err := p.publisher.Publish(ctx, verified)
	videoPublishMS := time.Since(videoPublishStarted).Milliseconds()
	if err != nil {
		return fmt.Errorf("publish overlay video: %w", err)
	}
	artifact.DriveFileID = loc.FileID
	artifact.DriveLink = loc.WebViewLink
	artifact.DriveFolderID = loc.FolderID

	if err := p.publishReceipt(ctx, spec, artifact, rootFolderID, scriptName, language, artifactID, filename, videoPublishMS); err != nil {
		return fmt.Errorf("publish overlay timing receipt: %w", err)
	}
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
	// Hash while streaming: the certified bytes are hashed by the same
	// io.Copy that writes them, so the staging file is never re-read from
	// disk to verify its digest.
	hasher := digest.NewSHA256()
	written, err := io.Copy(io.MultiWriter(file, hasher), resp.Body)
	if err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
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

var _ scriptgen.OverlayArtifactPublisher = (*DriveOverlayArtifactPublisher)(nil)
