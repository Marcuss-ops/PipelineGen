// Package scripts — curation types extracted from types.go (PG-029, June 2026).
package scripts

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// ── MediaCurator ─────────────────────────────────────────────────────────

// MediaCurator orchestrates semantic clip search + script generation.
// All fields are concrete typed.
type MediaCurator struct {
	// vectorStore field removed from this flow (PG-034, June 2026).
	// PJ-CURATE-1 (June 2026): clipSearch is the typed port for
	// the optional semantic-search leg. nil → curator consumes only
	// req.HintClipIDs. Set via SetClipSearchPort from the composition
	// root when Qdrant is enabled.
	serverURL     string
	clipsRepo     interface{} // *assets.ClipsRepository (avoid import cycle)
	clipBuilder   *ClipSourceBuilder
	generateOneUC *GenerateOneUseCase
	clipSearch    ClipSearchPort
	log           *zap.Logger
}

// ── CurateRequest / CurateResult ─────────────────────────────────────────

// CurateRequest carries the inputs for a curation job.
type CurateRequest struct {
	Query             string
	Title             string
	Language          string
	Tone              string
	Model             string
	MaxClips          int
	SelectableClips   int
	TargetWords       int
	MaxCharsPerScene  int
	MinScore          float64
	Source            string
	MediaType         string
	Type              string
	Style             string
	StyleInstructions string
	ForceRefresh      bool
	// PG-034 (June 2026): HintClipIDs lets callers seed a curation with
	// pre-resolved clip IDs from upstream sources, replacing the deleted
	// Qdrant semantic-search leg.
	HintClipIDs []string
	// PJ-CURATE-1 (June 2026): Search opt-in to the ClipSearchPort.
	Search bool
	// PJ-CURATE-1: AllowTextOnly opts back into the legacy
	// text-only fallback when both port and hint list are empty.
	// Defaults to false → ErrCurateNoClips surfaces on empty
	// resolution.
	AllowTextOnly bool
}

// CurateResult holds the output of a curation run.
type CurateResult struct {
	Title             string
	ClipScenes        []ClipScene
	Script            string
	WordCount         int
	CacheStatus       string
	AcceptedClipIDs   []string
	NarrativePlan     *NarrativePlan
	SourceText        string
	SourceFingerprint string
	SearchResults     []SearchResultInfo
	Timings           CurateTimings
}

// CurateTimings holds timing metrics for curation phases.
type CurateTimings struct {
	SearchMs      int64
	BuildCtxMs    int64
	WriteScriptMs int64
	TotalMs       int64
}

// SearchResultInfo holds a single search result.
type SearchResultInfo struct {
	ClipID    string
	Name      string
	Score     float64
	Source    string
	DriveLink string
}

// ClipScene represents a single scene with an associated clip.
type ClipScene struct {
	SceneIndex int
	Text       string
	ClipID     string
	DriveLink  string
	Kind       string
}

// ── Narrative structures ─────────────────────────────────────────────────

// NarrativePlan holds the narrative structure plan.
type NarrativePlan struct {
	Title        string             `json:"title"`
	Sections     []NarrativeSection `json:"sections"`
	TotalWords   int                `json:"total_words"`
	Style        string             `json:"style"`
	Relationship string             `json:"relationship"`
}

// NarrativeSection is one section of a narrative plan.
type NarrativeSection struct {
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	WordBudget int    `json:"word_budget"`
	KeyPoints  string `json:"key_points"`
}

// ── Curate error contract ────────────────────────────────────────────────

// ErrCurateNoClips is the sentinel for "no clips resolved (search
// returned nothing AND hint list was empty) and the caller did not
// opt in to the legacy text-only fallback". Use errors.Is to detect;
// for structured details (Query, MinScore, ResultCount) use errors.As
// to extract *CurateNoClipsError below.
//
// PJ-CURATE-1 (June 2026): replaces the previous silent text-only
// fallback that the audit verdict flagged as a semantic hijack — a
// /curate request that asked for clips and got zero clips would
// receive a text-only script with `accepted_clip_ids = []` and
// `voiceover_results = []`, leaving the client unable to distinguish
// "no clips found" from "intentional text-only mode". The new
// contract forces the caller to pass allow_text_only=true explicitly
// when they want the legacy behaviour, so a real failure surfaces
// as a typed error → job FAILED → operator dashboard visibility.
var ErrCurateNoClips = errors.New("curate: no clips found for query and text-only fallback opted out")

// CurateNoClipsError carries the structured details behind
// ErrCurateNoClips. Workers and tests call errors.As to extract and
// surface the fields; dashboards can show ResultCount=0 vs a
// non-zero search-with-filters case explicitly.
type CurateNoClipsError struct {
	Query       string
	MinScore    float64
	ResultCount int
}

func (e *CurateNoClipsError) Error() string {
	if e == nil {
		return ErrCurateNoClips.Error()
	}
	return fmt.Sprintf("%s: query=%q min_score=%.2f results=%d",
		ErrCurateNoClips.Error(), e.Query, e.MinScore, e.ResultCount)
}

// Unwrap lets errors.Is(err, ErrCurateNoClips) succeed.
func (e *CurateNoClipsError) Unwrap() error { return ErrCurateNoClips }
