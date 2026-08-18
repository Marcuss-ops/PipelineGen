package app

// localization_subtitle.go wires the localization capability's subtitle ports
// to the concrete TextTrack store + canonical ASS generator:
//
//   - localizationSubtitleResolver — resolves the translated text track
//     referenced by a LocalizedClipPlan (SubtitleTrackID + SubtitleSHA256)
//     into its timed cues, fail-closed on status/hash.
//   - localizationSubtitleCompiler — compiles the resolved cues into a
//     deterministic .ass via texttracks.CompileASSContent (the single owner
//     of ASS content generation).
//
// godlike/06 SSOT: the plan references the translated text ONLY by
// (SubtitleTrackID, SubtitleSHA256); the raw text stays owned by
// asset.TextTrack. These adapters fetch the content by those references and
// produce the burnable ASS — never duplicating the translation.
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── SubtitleResolver (TextTrack → cues) ─────────────────────────────

// textTrackByIDReader is the narrow read seam the resolver needs. The
// concrete *texttracks.TextTrackRepositorySQLite satisfies it via FindByID
// (PK → track + cues).
type textTrackByIDReader interface {
	FindByID(ctx context.Context, trackID int64) (*asset.TextTrack, []asset.TimedCue, error)
}

// localizationSubtitleResolver implements localization.SubtitleResolver with
// the canonical PK → (track, cues) fetch. Fail-closed: a non-READY track, a
// text-hash mismatch against the plan's SubtitleSHA256, or a track without
// timed cues never yields a burnable subtitle.
type localizationSubtitleResolver struct {
	tracks textTrackByIDReader
}

// newLocalizationSubtitleResolver builds the resolver. Fail-closed at call
// time (not construction): the nil check lives in ResolveSubtitleTrack so the
// composition root can build adapters independently of wiring order.
func newLocalizationSubtitleResolver(tracks textTrackByIDReader) *localizationSubtitleResolver {
	return &localizationSubtitleResolver{tracks: tracks}
}

func (r *localizationSubtitleResolver) ResolveSubtitleTrack(ctx context.Context, trackID int64, expectedSHA256 string) (*localization.ResolvedSubtitleTrack, error) {
	if r == nil || r.tracks == nil {
		return nil, errors.New("localization: subtitle resolver not wired")
	}
	track, cues, err := r.tracks.FindByID(ctx, trackID)
	if err != nil {
		return nil, fmt.Errorf("localization: resolve subtitle track %d: %w", trackID, err)
	}
	if track == nil {
		return nil, fmt.Errorf("localization: subtitle track %d not found", trackID)
	}
	// godlike/07 fail-closed: a non-READY row is not authoritative — burning
	// its (possibly empty or stale) text would corrupt the render.
	if track.Status != asset.TextTrackReady {
		return nil, fmt.Errorf("localization: subtitle track %d is not READY (status %q)", trackID, track.Status)
	}
	// godlike/06 SSOT: TextHash is the canonical asset.TextHash (text +
	// language + kind). Recompute only when the persisted value is empty
	// (legacy row) — never re-derive when a canonical hash is already present.
	hash := track.TextHash
	if hash == "" {
		hash = asset.TextHash(track.TextContent, track.LanguageCode, track.TextKind)
	}
	// godlike/07 fail-closed: the resolved text must match the plan's hash, or
	// the render would burn the wrong language's subtitles.
	if hash != expectedSHA256 {
		return nil, fmt.Errorf("localization: subtitle track %d text hash mismatch: resolved %q, plan %q", trackID, hash, expectedSHA256)
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("localization: subtitle track %d has no timed cues", trackID)
	}
	return &localization.ResolvedSubtitleTrack{
		TrackID:      track.ID,
		LanguageCode: track.LanguageCode,
		Cues:         cues,
		TextHash:     hash,
	}, nil
}

// ── SubtitleArtifactCompiler (cues → .ass) ──────────────────────────

// localizationSubtitleCompiler implements localization.SubtitleArtifactCompiler
// with the canonical ASS generator (texttracks.CompileASSContent — the single
// owner of ASS content generation). Identical cues + style ALWAYS produce
// identical bytes (the generator embeds no timestamps/randoms/absolute paths);
// the artifact is validated before it is returned.
type localizationSubtitleCompiler struct{}

func newLocalizationSubtitleCompiler() *localizationSubtitleCompiler {
	return &localizationSubtitleCompiler{}
}

func (c *localizationSubtitleCompiler) Compile(_ context.Context, in localization.SubtitleCompileInput) (*localization.SubtitleAsset, error) {
	if len(in.Cues) == 0 {
		return nil, errors.New("localization: subtitle compile: zero cues — subtitles cannot be compiled without timing (speech recognition is never regenerated for subtitles)")
	}
	content, err := texttracks.CompileASSContent(in.Cues, in.StyleHash)
	if err != nil {
		return nil, fmt.Errorf("localization: subtitle compile: %w", err)
	}

	outDir := strings.TrimSpace(in.OutputDir)
	if outDir == "" {
		outDir = "data/media/subtitles"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("localization: subtitle compile: mkdir: %w", err)
	}
	lang := strings.TrimSpace(in.Language)
	if lang == "" {
		lang = "und"
	}
	// Deterministic per-(clip, language) filename: re-compiling the same plan
	// idempotently overwrites the same artifact (no hash-in-name churn).
	localPath := filepath.Join(outDir, fmt.Sprintf("%s.%s.ass", in.ClipID, lang))
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("localization: subtitle compile: write ASS %q: %w", localPath, err)
	}
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	if err := texttracks.ValidateASSFile(localPath, in.ClipDurationMS); err != nil {
		return nil, fmt.Errorf("localization: subtitle compile: invalid generated ASS for %q: %w", in.ClipID, err)
	}
	return &localization.SubtitleAsset{
		LocalPath: localPath,
		SHA256:    sha,
		StyleHash: in.StyleHash,
		TrackID:   in.TrackID,
	}, nil
}
