package adapters

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── SubtitleResolver (TextTrack → cues) ─────────────────────────────

// SubtitleTrackReader is the narrow read seam the resolver needs. The
// concrete *texttracks.TextTrackRepositorySQLite satisfies it via FindByID
// (PK → track + cues).
type SubtitleTrackReader interface {
	FindByID(ctx context.Context, trackID int64) (*detail.TextTrack, []detail.TimedCue, error)
}

// SubtitleResolver implements localization.SubtitleResolver with the
// canonical PK → (track, cues) fetch. Fail-closed: a non-READY track, a
// text-hash mismatch against the plan's SubtitleSHA256, or a track without
// timed cues never yields a burnable subtitle.
type SubtitleResolver struct {
	tracks SubtitleTrackReader
}

// NewSubtitleResolver builds the resolver. Fail-closed at call time (not
// construction): the nil check lives in ResolveSubtitleTrack.
func NewSubtitleResolver(tracks SubtitleTrackReader) *SubtitleResolver {
	return &SubtitleResolver{tracks: tracks}
}

func (r *SubtitleResolver) ResolveSubtitleTrack(ctx context.Context, trackID int64, expectedSHA256 string) (*localization.ResolvedSubtitleTrack, error) {
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
	if track.Status != detail.TextTrackReady {
		return nil, fmt.Errorf("localization: subtitle track %d is not READY (status %q)", trackID, track.Status)
	}
	// godlike/06 SSOT: TextHash is the canonical detail.TextHash (text +
	// language + kind). Recompute only when the persisted value is empty
	// (legacy row) — never re-derive when a canonical hash is already present.
	hash := track.TextHash
	if hash == "" {
		hash = detail.TextHash(track.TextContent, track.LanguageCode, track.TextKind)
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

// SubtitleCompiler implements localization.SubtitleArtifactCompiler with the
// canonical ASS generator (texttracks.CompileASSContent — the single owner of
// ASS content generation). Identical cues + style ALWAYS produce identical
// bytes (the generator embeds no timestamps/randoms/absolute paths); the
// artifact is validated before it is returned.
type SubtitleCompiler struct{}

// NewSubtitleCompiler builds the subtitle compiler adapter.
func NewSubtitleCompiler() *SubtitleCompiler {
	return &SubtitleCompiler{}
}

func (c *SubtitleCompiler) Compile(_ context.Context, in localization.SubtitleCompileInput) (*localization.SubtitleAsset, error) {
	if len(in.Cues) == 0 {
		return nil, errors.New("localization: subtitle compile: zero cues — subtitles cannot be compiled without timing (speech recognition is never regenerated for subtitles)")
	}
	// A transcript can contain a final cue from the original source timeline
	// beyond the extracted clip boundary. Keep the transcript in the DB, but
	// clip only the rendered interval so ASS validation matches the media.
	// A positive duration is required here because the generated ASS is tied
	// to a concrete media artifact; callers without a duration must fail closed.
	if in.ClipDurationMS <= 0 {
		return nil, errors.New("localization: subtitle compile: clip duration is required")
	}
	cues := trimLocalizationCues(in.Cues, in.ClipDurationMS)
	if len(cues) == 0 {
		return nil, errors.New("localization: subtitle compile: no cues remain inside clip duration")
	}
	content, err := texttracks.CompileASSContent(cues, in.StyleHash)
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
	sha := digest.SHA256Bytes([]byte(content))
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

func trimLocalizationCues(in []detail.TimedCue, durationMS int64) []detail.TimedCue {
	if durationMS <= 0 {
		return append([]detail.TimedCue(nil), in...)
	}
	out := make([]detail.TimedCue, 0, len(in))
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
