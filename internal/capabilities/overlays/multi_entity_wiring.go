package overlays

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// MultiEntityKind classifies the elements within a temporally grouped scene.
type MultiEntityKind string

const (
	MultiEntityKindImage  MultiEntityKind = "image"
	MultiEntityKindPhrase MultiEntityKind = "phrase"
	MultiEntityKindMixed  MultiEntityKind = "mixed"
)

// MultiEntityCandidate is one grounded image or phrase that may share a
// presentation window with nearby candidates.
type MultiEntityCandidate struct {
	ID       string
	SceneID  string
	Kind     MultiEntityKind
	Name     string
	Text     string
	AssetRef *capmodel.OverlayAssetRef
	StartMS  int64
	EndMS    int64
	Priority int
}

// MultiEntityGroup is the editorial source for a composite image sequence.
// Image candidates share one group choreography and keep their own captions;
// phrase content stays on the existing important_phrase primitive.
//
// Grouping and lowering are Rust-owned: the scripts projection passes the
// authored group to the editorial plan request, and overlay-compiler applies
// the choreography.
type MultiEntityGroup struct {
	GroupID       string
	SceneID       string
	GroupType     MultiEntityKind
	Count         int
	Items         []MultiEntityCandidate
	StartMS       int64
	EndMS         int64
	FocusStrategy string
}
