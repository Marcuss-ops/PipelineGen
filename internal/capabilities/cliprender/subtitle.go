package cliprender

// subtitle.go owns the resolved ASS artifact contract referenced by
// ClipRenderPlanV1. The ASS is compiled from the canonical transcript cues
// BEFORE the plan is sealed — Rust never regenerates speech recognition or
// subtitle content (feature spec §5).
//
// The capability defines the narrow port; the composition root wires the
// concrete compiler. The canonical ASS generator already exists in the
// project (internal/application/assets/texttracks/ass_materializer.go) and is
// the single owner of ASS content generation — the port exists so this
// capability stays free of application-layer imports.

import (
	"context"
	"errors"
)

// ErrSubtitleCompileUnavailable is the typed sentinel when subtitle
// compilation is required (subtitles.enabled=true) but no compiler is wired
// or the compilation fails. Fail-closed: the plan is never sealed with a
// missing or stale ASS artifact.
var ErrSubtitleCompileUnavailable = errors.New("clip.render: subtitle ASS compilation unavailable")

// SubtitleCompileInput is the typed input for the SubtitleCompiler. Cues come
// verbatim from the canonical transcript (never re-derived). OutputDir is the
// scratch directory the compiler must write the deterministic .ass into.
type SubtitleCompileInput struct {
	RunID          string
	AssetID        string
	Language       string
	Mode           string // burn | sidecar
	StyleID        string
	Cues           []Cue
	ClipDurationMS int64
	SourceSHA256   string
	OutputDir      string
}

// SubtitleArtifact is the resolved ASS artifact referenced by the plan.
// LocalPath + SHA256 are verified before the plan is sealed.
type SubtitleArtifact struct {
	LocalPath string
	SHA256    string
	Mode      string
	StyleID   string
}

// SubtitleCompiler compiles a deterministic .ass artifact from the canonical
// transcript cues. Implementations must be deterministic: identical cues +
// style produce identical bytes.
type SubtitleCompiler interface {
	Compile(ctx context.Context, in SubtitleCompileInput) (*SubtitleArtifact, error)
}
