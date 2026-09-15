package adapters

// cliprender_publisher_async.go owns the durable-side helpers of the clip.render
// publication boundary: staging the rendered video and its optional ASS sidecar
// outside the ephemeral job workspace, and the idempotent reuse of an
// already-staged artifact.
//
// It was split out of cliprender_publisher.go when the strict per-file LOC gate
// rejected the combined file; the synchronous publication path and
// publishAsyncDrive stay in cliprender_publisher.go.

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// artifactExtension resolves the file extension for the published artifact. A
// locally materialized path supplies it directly; a locator-only artifact
// derives it from the certified content type, then from the URL path, and
// falls back to ".mp4" (the only clip.render output contract today).
func artifactExtension(localPath, contentType, artifactURL string) string {
	if ext := filepath.Ext(localPath); ext != "" {
		return ext
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch mediaType {
	case "video/mp4":
		return ".mp4"
	case "video/x-matroska":
		return ".mkv"
	case "video/webm":
		return ".webm"
	}
	if parsed, err := url.Parse(strings.TrimSpace(artifactURL)); err == nil {
		if ext := filepath.Ext(parsed.Path); ext != "" {
			return ext
		}
	}
	return ".mp4"
}

// asyncStagingDirectory resolves and creates the durable root that detaches
// rendered artifacts from the ephemeral per-job workspace. The outbox event may
// be processed after the job runner has cleaned that workspace, so the payload
// must reference a copy outside it.
func (p *ClipRenderPublisher) asyncStagingDirectory() (string, error) {
	root := strings.TrimSpace(p.asyncStagingRoot)
	if root == "" {
		root = filepath.Join(os.TempDir(), "pipelinegen", "cliprender", "staging")
	}
	if !filepath.IsAbs(root) {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve staging root %q: %w", root, err)
		}
		root = absolute
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create staging root %q: %w", root, err)
	}
	return root, nil
}

// detachToStaging moves source onto destination, preferring an atomic rename
// and falling back to a synced copy when the workspace and the staging root are
// on different mounts. A failed copy never leaves a partial destination behind.
func (p *ClipRenderPublisher) detachToStaging(source, destination string) error {
	if source == destination {
		return nil
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	inFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source artifact %q: %w", source, err)
	}
	outFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		_ = inFile.Close()
		return fmt.Errorf("create staged artifact %q: %w", destination, err)
	}
	_, copyErr := io.Copy(outFile, inFile)
	if copyErr == nil {
		copyErr = outFile.Sync()
	}
	closeOutErr := outFile.Close()
	closeInErr := inFile.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("copy artifact to staging: %w", copyErr)
	}
	if closeOutErr != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("close staged artifact: %w", closeOutErr)
	}
	if closeInErr != nil {
		return fmt.Errorf("close source artifact: %w", closeInErr)
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove workspace artifact after staging: %w", err)
	}
	return nil
}

// stageAsyncArtifact atomically detaches the rendered video from the ephemeral
// job workspace.
func (p *ClipRenderPublisher) stageAsyncArtifact(source, assetID string, size int64) (string, error) {
	root, err := p.asyncStagingDirectory()
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(source)
	if ext == "" {
		ext = ".mp4"
	}
	destination := filepath.Join(root, assetID+ext)
	if source == destination {
		return destination, nil
	}
	if _, statErr := os.Stat(destination); statErr == nil {
		return p.reuseStagedArtifact(source, destination, assetID, size)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect staged artifact %q: %w", destination, statErr)
	}
	if err := p.detachToStaging(source, destination); err != nil {
		return "", err
	}
	return destination, nil
}

// stageAsyncSidecar detaches the compiled ASS sidecar from the transient
// workspace. It is idempotent: a rerun that finds the same content-addressed
// sidecar already staged verifies the digest and reuses it.
func (p *ClipRenderPublisher) stageAsyncSidecar(source, assetID, sha256 string, size int64) (string, error) {
	root, err := p.asyncStagingDirectory()
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, assetID+".ass")
	if source == destination {
		return destination, nil
	}
	if info, statErr := os.Stat(destination); statErr == nil {
		if size > 0 && info.Size() != size {
			return "", fmt.Errorf("existing staged sidecar %q size=%d want=%d", destination, info.Size(), size)
		}
		if strings.TrimSpace(sha256) != "" {
			got, _, hashErr := digest.SHA256File(destination)
			if hashErr != nil {
				return "", fmt.Errorf("verify existing staged sidecar: %w", hashErr)
			}
			if !strings.EqualFold(got, sha256) {
				return "", fmt.Errorf("existing staged sidecar %q does not match the declared digest", destination)
			}
		}
		if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove duplicate workspace sidecar: %w", err)
		}
		return destination, nil
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect staged sidecar %q: %w", destination, statErr)
	}
	if err := p.detachToStaging(source, destination); err != nil {
		return "", err
	}
	return destination, nil
}

// stageAsyncSubtitle projects the compiled ASS artifact into the delivery
// intent. It returns nil when the request did not select sidecar subtitles, or
// when the canonical ASS already exists in Drive (DriveFileID set) — in that
// case there is nothing to upload for this render.
func (p *ClipRenderPublisher) stageAsyncSubtitle(in cliprender.RenderPublishInput, assetID string, enabled bool) (*cliprender.ClipRenderSubtitleDelivery, error) {
	if !enabled || in.Subtitles == nil {
		return nil, nil
	}
	if strings.TrimSpace(in.Subtitles.LocalPath) == "" {
		return nil, fmt.Errorf("clip.render publisher: sidecar subtitles requested but the ASS artifact has no local path")
	}
	if strings.TrimSpace(in.Subtitles.DriveFileID) != "" {
		return nil, nil
	}
	info, err := os.Stat(in.Subtitles.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("clip.render publisher: inspect sidecar artifact: %w", err)
	}
	if info.IsDir() || info.Size() <= 0 {
		return nil, fmt.Errorf("clip.render publisher: sidecar artifact %q is empty", in.Subtitles.LocalPath)
	}
	staged, err := p.stageAsyncSidecar(in.Subtitles.LocalPath, assetID, in.Subtitles.SHA256, info.Size())
	if err != nil {
		return nil, fmt.Errorf("clip.render publisher: stage subtitle sidecar: %w", err)
	}
	language := "und"
	textHash := ""
	if in.Transcript != nil {
		if lang := strings.TrimSpace(in.Transcript.Language); lang != "" {
			language = lang
		}
		textHash = in.Transcript.TextSHA256
	}
	return &cliprender.ClipRenderSubtitleDelivery{
		LocalPath:    staged,
		Filename:     sidecarFilename(assetID, in.SourceTitle, languageFilenameTag(in.Transcript)),
		SHA256:       strings.ToLower(strings.TrimSpace(in.Subtitles.SHA256)),
		SizeBytes:    info.Size(),
		LanguageCode: language,
		TextHash:     textHash,
		StyleVersion: in.Subtitles.StyleID,
	}, nil
}

// sidecarFilename mirrors the video filename convention: the human source
// title when available, otherwise the content-addressed asset id. The language
// tag keeps the per-language sidecars distinct for the same reason the video
// filename carries it.
func sidecarFilename(assetID, sourceTitle, languageTag string) string {
	base := assetID
	if strings.TrimSpace(sourceTitle) != "" {
		safe := textutil.SanitizeFilename(sourceTitle)
		if safe != "" && safe != "unnamed" {
			base = safe
		}
	}
	if languageTag != "" {
		base += "_" + languageTag
	}
	return base + ".ass"
}

func (p *ClipRenderPublisher) reuseStagedArtifact(source, destination, assetID string, size int64) (string, error) {
	stagedHash, stagedSize, err := digest.SHA256File(destination)
	if err != nil {
		return "", fmt.Errorf("verify existing staged artifact: %w", err)
	}
	prefixLen := len(assetID) - len("cliprender_")
	if prefixLen <= 0 || len(stagedHash) < prefixLen || stagedSize != size || !strings.HasPrefix(assetID, "cliprender_") || !strings.HasPrefix(assetID[len("cliprender_"):], stagedHash[:prefixLen]) {
		return "", fmt.Errorf("existing staged artifact %q does not match asset %q", destination, assetID)
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) && source != destination {
		return "", fmt.Errorf("remove duplicate workspace artifact: %w", err)
	}
	return destination, nil
}
