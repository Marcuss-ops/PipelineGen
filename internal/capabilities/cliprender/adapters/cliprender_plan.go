package adapters

// cliprender_plan.go wires the deterministic ASS artifact compiler used by
// clip.render. Rendering itself is owned by the RenderingGen/Chronon executor
// in internal/platform/renderinggen; this package contains no render engine
// adapter.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ClipRenderSubtitleCompiler implements the capability's SubtitleCompiler
// port with the canonical ASS generator. The artifact is written into the
// run's scratch dir and validated before the plan is sealed.
type ClipRenderSubtitleCompiler struct {
	artifacts detail.SubtitleArtifactRepository
}

// SetArtifactRepository attaches the canonical subtitle-artifact registry.
// A current READY artifact is reused only when its local bytes, hash, style,
// duration and Drive publication are all still valid.
func (c *ClipRenderSubtitleCompiler) SetArtifactRepository(repo detail.SubtitleArtifactRepository) {
	if c != nil {
		c.artifacts = repo
	}
}

// assContentCache reuses deterministic ASS generation across repeated renders
// of the same transcript/style. It is a content cache, not a render cache.
var assContentCache sync.Map // map[string]string

func (c *ClipRenderSubtitleCompiler) Compile(ctx context.Context, in cliprender.SubtitleCompileInput) (*cliprender.SubtitleArtifact, error) {
	switch in.Mode {
	case cliprender.SubtitlesModeBurn, cliprender.SubtitlesModeSidecar:
	default:
		return nil, fmt.Errorf("%w: invalid subtitle mode %q", cliprender.ErrSubtitleCompileUnavailable, in.Mode)
	}
	if len(in.Cues) == 0 {
		return nil, fmt.Errorf("%w: zero cues for asset %q — speech recognition is never regenerated for subtitles", cliprender.ErrSubtitleCompileUnavailable, in.AssetID)
	}
	if c != nil && c.artifacts != nil {
		current, err := c.artifacts.FindCurrent(ctx, in.AssetID, in.Language, detail.SubtitleFormatASS)
		if err != nil {
			return nil, fmt.Errorf("%w: lookup current ASS artifact: %v", cliprender.ErrSubtitleCompileUnavailable, err)
		}
		if current != nil {
			if current.Status != detail.SubtitleStatusReady || current.StyleVersion != in.StyleID ||
				current.LocalPath == "" || current.DriveFileID == "" {
				return nil, fmt.Errorf("%w: current ASS artifact for %q is not reusable (status=%s style=%q local=%t drive=%t)",
					cliprender.ErrSubtitleCompileUnavailable, in.AssetID, current.Status, current.StyleVersion,
					current.LocalPath != "", current.DriveFileID != "")
			}
			if _, err := os.Stat(current.LocalPath); err != nil {
				return nil, fmt.Errorf("%w: current ASS artifact for %q is missing locally: %v", cliprender.ErrSubtitleCompileUnavailable, in.AssetID, err)
			}
			if err := texttracks.ValidateASSFile(current.LocalPath, in.ClipDurationMS); err != nil {
				return nil, fmt.Errorf("%w: current ASS artifact for %q failed validation: %v", cliprender.ErrSubtitleCompileUnavailable, in.AssetID, err)
			}
			sha, _, err := digest.SHA256File(current.LocalPath)
			if err != nil || sha != current.LegacyFileMD5 {
				return nil, fmt.Errorf("%w: current ASS artifact for %q failed hash verification", cliprender.ErrSubtitleCompileUnavailable, in.AssetID)
			}
			cliprender.RecordSubtitleCacheFacts(current.LocalPath, cliprender.SubtitleCacheFacts{ContentCacheHit: true, ArtifactCacheHit: true, Measured: true})
			return &cliprender.SubtitleArtifact{LocalPath: current.LocalPath, SHA256: sha, Mode: in.Mode, StyleID: current.StyleVersion, DriveFileID: current.DriveFileID, DriveLink: current.DriveURL}, nil
		}
	}
	cues := trimClipRenderCues(in.Cues, in.ClipDurationMS)
	if len(cues) == 0 {
		return nil, fmt.Errorf("%w: no cues remain inside clip duration for asset %q", cliprender.ErrSubtitleCompileUnavailable, in.AssetID)
	}
	canonicalCues := mapClipRenderCues(cues)
	key := digest.SHA256String(fmt.Sprintf("%s\x00%s\x00%v", in.Language, in.StyleID, canonicalCues))
	var content string
	contentCacheHit := false
	if cached, ok := assContentCache.Load(key); ok {
		content = cached.(string)
		contentCacheHit = true
	} else {
		generated, err := texttracks.CompileASSContent(canonicalCues, in.StyleID)
		if err != nil {
			return nil, fmt.Errorf("%w: compile ASS content: %v", cliprender.ErrSubtitleCompileUnavailable, err)
		}
		content = generated
		assContentCache.Store(key, content)
	}
	if err := os.MkdirAll(in.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create subtitle output dir %q: %w", in.OutputDir, err)
	}
	localPath := filepath.Join(in.OutputDir, "subtitles.ass")
	sha := digest.SHA256String(content)
	artifactCacheHit := false
	if existing, err := os.ReadFile(localPath); err == nil && string(existing) == content {
		artifactCacheHit = true
		if err := texttracks.ValidateASSFile(localPath, in.ClipDurationMS); err != nil {
			return nil, fmt.Errorf("%w: invalid existing ASS: %v", cliprender.ErrSubtitleCompileUnavailable, err)
		}
		cliprender.RecordSubtitleCacheFacts(localPath, cliprender.SubtitleCacheFacts{ContentCacheHit: contentCacheHit, ArtifactCacheHit: artifactCacheHit, Measured: true})
		return &cliprender.SubtitleArtifact{LocalPath: localPath, SHA256: sha, Mode: in.Mode, StyleID: in.StyleID}, nil
	}
	cacheDir := filepath.Join(filepath.Dir(filepath.Dir(in.OutputDir)), "subtitle-cache")
	cachePath := filepath.Join(cacheDir, sha+".ass")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create subtitle cache dir %q: %w", cacheDir, err)
	}
	if _, err := os.Stat(cachePath); err == nil {
		artifactCacheHit = true
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(cachePath, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write cached ASS artifact %q: %w", cachePath, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat cached ASS artifact %q: %w", cachePath, err)
	}
	if err := os.Link(cachePath, localPath); err != nil {
		if copyErr := os.WriteFile(localPath, []byte(content), 0o644); copyErr != nil {
			return nil, fmt.Errorf("link/copy ASS artifact %q: link=%v copy=%w", localPath, err, copyErr)
		}
	}
	if err := texttracks.ValidateASSFile(localPath, in.ClipDurationMS); err != nil {
		return nil, fmt.Errorf("%w: invalid generated ASS: %v", cliprender.ErrSubtitleCompileUnavailable, err)
	}
	cliprender.RecordSubtitleCacheFacts(localPath, cliprender.SubtitleCacheFacts{ContentCacheHit: contentCacheHit, ArtifactCacheHit: artifactCacheHit, Measured: true})
	return &cliprender.SubtitleArtifact{LocalPath: localPath, SHA256: sha, Mode: in.Mode, StyleID: in.StyleID}, nil
}

func trimClipRenderCues(in []cliprender.Cue, durationMS int64) []cliprender.Cue {
	if durationMS <= 0 {
		return append([]cliprender.Cue(nil), in...)
	}
	out := make([]cliprender.Cue, 0, len(in))
	for _, cue := range in {
		if cue.StartMs >= durationMS {
			continue
		}
		if cue.EndMs > durationMS {
			cue.EndMs = durationMS
		}
		if cue.EndMs > cue.StartMs {
			out = append(out, cue)
		}
	}
	return out
}
