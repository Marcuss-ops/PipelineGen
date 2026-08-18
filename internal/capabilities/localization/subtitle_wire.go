package localization

// subtitle_wire.go owns the canonical TextTrack → ASS wiring: it resolves the
// translated text track referenced by a LocalizedClipPlan and compiles it into
// a deterministic .ass artifact for the render pass.
//
// godlike/06 SSOT (one canonical owner per fact): the plan references the
// translated text ONLY by (SubtitleTrackID, SubtitleSHA256) — never the raw
// text. The translation stays owned by asset.TextTrack; this wire fetches the
// content by those references, verifies the content hash, and produces the
// ASS. The raw translated text is therefore never duplicated across DB /
// request / render plan / document model.
//
// godlike/07 fail-closed: a track whose text hash does not match the plan's
// SubtitleSHA256 (wrong language, tampered, or stale) is rejected BEFORE any
// ASS is written — Rust never burns subtitles from the wrong track.

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ResolvedSubtitleTrack is the fetched translated text-track content that
// feeds the ASS. TextHash is the resolver's verified digest and MUST equal
// the plan's SubtitleSHA256 before any ASS is compiled.
type ResolvedSubtitleTrack struct {
	TrackID      int64
	LanguageCode string
	Cues         []asset.TimedCue
	TextHash     string
}

// SubtitleResolver resolves the translated text track referenced by the plan
// (SubtitleTrackID + SubtitleSHA256) into its timed cues, verifying the
// content hash. The concrete adapter reads the canonical text-track + segments
// store; the capability never queries the DB itself.
type SubtitleResolver interface {
	ResolveSubtitleTrack(ctx context.Context, trackID int64, expectedSHA256 string) (*ResolvedSubtitleTrack, error)
}

// SubtitleAsset is the compiled .ass artifact the render pass burns.
// LocalPath + SHA256 are verified by the compiler before it returns them.
type SubtitleAsset struct {
	LocalPath string
	SHA256    string
	StyleHash string
	TrackID   int64
}

// SubtitleCompileInput is the resolved input for the ASS compiler. Cues come
// verbatim from the resolved track (never re-derived); OutputDir is the
// scratch directory the deterministic .ass lands in.
type SubtitleCompileInput struct {
	ClipID         string
	Language       string
	StyleHash      string
	TrackID        int64
	Cues           []asset.TimedCue
	ClipDurationMS int64
	OutputDir      string
}

// SubtitleArtifactCompiler compiles resolved cues into a deterministic .ass.
// Identical cues + style MUST produce identical bytes (the render's
// wrong-language-contamination check depends on that determinism).
type SubtitleArtifactCompiler interface {
	Compile(ctx context.Context, in SubtitleCompileInput) (*SubtitleAsset, error)
}

// SubtitleWire is the canonical TextTrack → ASS wiring. It is immutable after
// construction and safe for concurrent Wire calls.
type SubtitleWire struct {
	tracks   SubtitleResolver
	compiler SubtitleArtifactCompiler
	workDir  string
}

// NewSubtitleWire builds the canonical wire. Fail-closed: both ports are
// mandatory — a wire that cannot resolve the track or compile the ASS can
// never produce a burnable subtitle artifact.
func NewSubtitleWire(tracks SubtitleResolver, compiler SubtitleArtifactCompiler, workDir string) (*SubtitleWire, error) {
	if tracks == nil {
		return nil, fmt.Errorf("localization.NewSubtitleWire: subtitle resolver is required")
	}
	if compiler == nil {
		return nil, fmt.Errorf("localization.NewSubtitleWire: subtitle compiler is required")
	}
	return &SubtitleWire{tracks: tracks, compiler: compiler, workDir: workDir}, nil
}

// Wire resolves the plan's subtitle track and compiles its .ass artifact.
// Fail-closed: an invalid plan, an unresolvable track, a text-hash mismatch,
// or empty cues all abort before any ASS is written.
func (w *SubtitleWire) Wire(ctx context.Context, plan LocalizedClipPlan) (*SubtitleAsset, error) {
	if w == nil || w.tracks == nil || w.compiler == nil {
		return nil, fmt.Errorf("localization: subtitle wire is not initialized")
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("localization: subtitle wire: %w", err)
	}

	track, err := w.tracks.ResolveSubtitleTrack(ctx, plan.SubtitleTrackID, plan.SubtitleSHA256)
	if err != nil {
		return nil, fmt.Errorf("localization: subtitle wire: resolve track %d: %w", plan.SubtitleTrackID, err)
	}
	if track == nil {
		return nil, fmt.Errorf("localization: subtitle wire: subtitle track %d not found", plan.SubtitleTrackID)
	}
	// godlike/07 fail-closed: the resolved text must match the plan's hash, or
	// the render would burn the wrong language's subtitles.
	if track.TextHash != plan.SubtitleSHA256 {
		return nil, fmt.Errorf("localization: subtitle wire: subtitle track %d text hash mismatch: resolved %q, plan %q", plan.SubtitleTrackID, track.TextHash, plan.SubtitleSHA256)
	}
	if len(track.Cues) == 0 {
		return nil, fmt.Errorf("localization: subtitle wire: subtitle track %d has no timed cues", plan.SubtitleTrackID)
	}

	asset, err := w.compiler.Compile(ctx, SubtitleCompileInput{
		ClipID:         plan.ClipID,
		Language:       plan.TargetLanguage,
		StyleHash:      plan.SubtitleStyleHash,
		TrackID:        plan.SubtitleTrackID,
		Cues:           track.Cues,
		ClipDurationMS: plan.DurationMS,
		OutputDir:      w.workDir,
	})
	if err != nil {
		return nil, fmt.Errorf("localization: subtitle wire: compile ASS: %w", err)
	}
	if asset == nil || asset.LocalPath == "" || asset.SHA256 == "" {
		return nil, fmt.Errorf("localization: subtitle wire: compiler returned an incomplete ASS artifact")
	}
	return asset, nil
}
