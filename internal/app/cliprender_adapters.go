package app

// cliprender_adapters.go wires the concrete adapters for the clip.render
// parallel preparation phase. The capability (internal/capabilities/cliprender)
// owns the ports; THIS file (composition root) owns the mechanics:
//
//   - AssetResolver     → canonical asset registry (asset.Service)
//   - AssetMaterializer → local copy reuse + Drive download to scratch
//   - TranscriptResolver → canonical text-track repo (reuse) + AcquireService
//     (Whisper chain generation) + cue writer (persist)
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// hashFile and firstNonEmpty are shared with vidrush_materialization_wiring.go
// (same package) — do not redeclare them here.

// ── AssetResolver ────────────────────────────────────────────────────

// clipRenderAssetResolver maps a canonical asset_id to the capability's
// AssetRef via the canonical asset registry.
type clipRenderAssetResolver struct {
	assets *asset.Service
}

func (r *clipRenderAssetResolver) ResolveAsset(ctx context.Context, assetID string) (*cliprender.AssetRef, error) {
	if r.assets == nil {
		return nil, errors.New("clip.render: asset registry not wired")
	}
	details, err := r.assets.Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("load asset %q: %w", assetID, err)
	}
	if details == nil || details.Asset == nil {
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	a := details.Asset
	return &cliprender.AssetRef{
		AssetID:     a.ID,
		MediaType:   string(a.MediaType),
		LocalPath:   a.LocalPath(),
		DriveFileID: a.DriveFileID(),
		FileHash:    firstNonEmpty(a.Sha256(), a.FileHash(), a.ContentHash()),
		DurationMS:  a.Duration.Milliseconds(),
	}, nil
}

// ── AssetMaterializer ────────────────────────────────────────────────

// clipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
type clipRenderMaterializer struct {
	drive      drivepkg.Reader
	scratchDir string
}

func (m *clipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	// (1) Registered local copy.
	if ref.LocalPath != "" {
		if info, err := os.Stat(ref.LocalPath); err == nil && !info.IsDir() {
			sha, size, err := hashFile(ref.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("hash local source %q: %w", ref.LocalPath, err)
			}
			return &cliprender.MaterializedAsset{
				AssetID:    ref.AssetID,
				LocalPath:  ref.LocalPath,
				SHA256:     sha,
				SizeBytes:  size,
				DurationMS: ref.DurationMS,
				FromCache:  true,
			}, nil
		}
	}

	// (2/3) Drive materialization into scratch.
	if ref.DriveFileID == "" {
		return nil, fmt.Errorf("clip.render: asset %q has neither a local copy nor a Drive source", ref.AssetID)
	}
	if m.drive == nil {
		return nil, fmt.Errorf("clip.render: Drive reader not wired (asset %q requires Drive materialization)", ref.AssetID)
	}
	target := filepath.Join(m.scratchDir, "assets", ref.AssetID+".mp4")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		sha, size, err := hashFile(target)
		if err != nil {
			return nil, fmt.Errorf("hash cached source %q: %w", target, err)
		}
		return &cliprender.MaterializedAsset{
			AssetID:    ref.AssetID,
			LocalPath:  target,
			SHA256:     sha,
			SizeBytes:  size,
			DurationMS: ref.DurationMS,
			FromCache:  true,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	rc, _, err := m.drive.DownloadFile(ctx, ref.DriveFileID)
	if err != nil {
		return nil, fmt.Errorf("download asset %q from Drive: %w", ref.AssetID, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return nil, fmt.Errorf("create scratch file: %w", err)
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hasher), rc)
	closeErr := out.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("write scratch file %q: %w", target, copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close scratch file %q: %w", target, closeErr)
	}
	return &cliprender.MaterializedAsset{
		AssetID:    ref.AssetID,
		LocalPath:  target,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:  n,
		DurationMS: ref.DurationMS,
		FromCache:  false,
	}, nil
}

// ── TranscriptResolver ───────────────────────────────────────────────

// clipRenderTranscriptResolver reuses the canonical READY text track when it
// exists and generates (Whisper chain) + optionally persists when it does not.
type clipRenderTranscriptResolver struct {
	repo      asset.TextTrackRepository
	acquire   *texttracks.AcquireService
	cueWriter texttracks.TimedCueWriter
	log       *zap.Logger
}

func (r *clipRenderTranscriptResolver) Lookup(ctx context.Context, in cliprender.TranscriptInput) (*cliprender.TranscriptResult, bool, error) {
	if r.repo == nil {
		return nil, false, errors.New("clip.render: text track repository not wired")
	}
	track, cues, err := r.repo.FindReady(ctx, in.AssetID, in.Language, asset.TextTrackTranscript)
	if err != nil {
		return nil, false, err
	}
	if track == nil {
		return nil, false, nil
	}
	hash := track.TextHash
	if hash == "" {
		hash = asset.TextHash(track.TextContent, in.Language, asset.TextTrackTranscript)
	}
	return &cliprender.TranscriptResult{
		AssetID:           in.AssetID,
		Language:          in.Language,
		Text:              track.TextContent,
		Cues:              mapTimedCues(cues),
		TextSHA256:        hash,
		Reused:            true,
		SourceAudioSHA256: in.SourceSHA256,
	}, true, nil
}

func (r *clipRenderTranscriptResolver) Generate(ctx context.Context, in cliprender.TranscriptInput, source *cliprender.MaterializedAsset) (*cliprender.TranscriptResult, error) {
	if r.acquire == nil {
		return nil, fmt.Errorf("%w: Whisper acquisition service not wired", cliprender.ErrTranscriptGenerationUnavailable)
	}
	if source == nil || source.LocalPath == "" {
		return nil, fmt.Errorf("%w: no materialized source audio", cliprender.ErrTranscriptGenerationUnavailable)
	}
	result, err := r.acquire.Acquire(ctx, texttracks.AcquireCommand{
		AssetID:   in.AssetID,
		LocalPath: source.LocalPath,
		Language:  in.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("acquire transcript for %q: %w", in.AssetID, err)
	}
	if result == nil || (result.PlainText == "" && len(result.Cues) == 0) {
		return nil, fmt.Errorf("%w: empty transcript for %q", cliprender.ErrTranscriptGenerationUnavailable, in.AssetID)
	}
	lang := result.LanguageCode
	if lang == "" {
		lang = in.Language
	}
	hash := asset.TextHash(result.PlainText, lang, asset.TextTrackTranscript)

	if in.Persist {
		if err := r.persist(ctx, in.AssetID, lang, result, hash); err != nil {
			return nil, fmt.Errorf("persist transcript for %q: %w", in.AssetID, err)
		}
		if r.log != nil {
			r.log.Info("clip.render.transcript.persisted",
				zap.String("asset_id", in.AssetID),
				zap.String("language", lang),
				zap.Int("cues", len(result.Cues)),
			)
		}
	}

	return &cliprender.TranscriptResult{
		AssetID:           in.AssetID,
		Language:          lang,
		Text:              result.PlainText,
		Cues:              mapTimedCues(result.Cues),
		TextSHA256:        hash,
		Reused:            false,
		SourceAudioSHA256: in.SourceSHA256,
		DurationMS:        result.DurationMs,
	}, nil
}

// persist writes the generated transcript as a READY canonical text track
// (idempotent upsert on UNIQUE(asset_id, language_code, text_kind)) and the
// timed cues when present.
func (r *clipRenderTranscriptResolver) persist(
	ctx context.Context,
	assetID, lang string,
	result *texttracks.AcquireResult,
	textHash string,
) error {
	if r.repo == nil {
		return errors.New("text track repository not wired for persistence")
	}
	srcType := result.SourceType
	if srcType == "local_file" {
		srcType = asset.TextSourceProvided
	}
	track := asset.TextTrack{
		AssetID:            assetID,
		LanguageCode:       lang,
		TextKind:           asset.TextTrackTranscript,
		TextContent:        result.PlainText,
		SourceType:         srcType,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           clipRenderProviderFor(result.SourceType),
		TextHash:           textHash,
		Confidence:         result.Confidence,
		Status:             asset.TextTrackReady,
	}
	if err := r.repo.UpsertBatch(ctx, []asset.TextTrack{track}); err != nil {
		return err
	}
	if len(result.Cues) > 0 && r.cueWriter != nil {
		return r.cueWriter.ReplaceTranscriptCues(ctx, assetID, map[string][]asset.TimedCue{lang: result.Cues})
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func mapTimedCues(in []asset.TimedCue) []cliprender.Cue {
	if len(in) == 0 {
		return nil
	}
	out := make([]cliprender.Cue, 0, len(in))
	for _, c := range in {
		out = append(out, cliprender.Cue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return out
}

// clipRenderProviderFor maps the acquisition source_type to the canonical
// provider label persisted on the text track (whisper/youtube, else empty).
func clipRenderProviderFor(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceYouTubeSubtitle:
		return "youtube"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return ""
	}
}
