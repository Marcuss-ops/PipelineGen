// Package scripts — curation types extracted from types.go (PG-029, June 2026).
//
// PR 9 (June 2026) — DEPRECATION HEADER (Zero-Legacy §07):
//
//   - Deprecation ID:          DL-CURATIONTYPES-001
//   - Owner:                    internal/capabilities/scripts wave owner
//   - Replacement:              SourceCurate resolver + GenerateOneUseCase
//     (PR 4 + PR 5); every curate source now
//     routes through SourceRegistry →
//     GenerateOneUseCase, returning the canonical
//     *scriptpkg.GenerationResult. The legacy
//     types in this file are kept for back-compat
//     with MediaCurator.Curate (zero production
//     callers as of PR 9).
//   - Introduction date:        2026-06-27
//   - Removal deadline:         2026-09-27 (90-day grace; tracks metric
//     "media_curator_curate_calls_per_day" —
//     removal gate is zero for 30 consecutive
//     calendar days)
//   - Tracking issue:           Wave-12 owner ticket
//   - Compatibility test:       degrade_legacy_curate_call_returns_zero_results
//     (test asserts that a legacy /curate HTTP
//     call surfaces typed error rather than
//     silently producing an empty script)
//   - Usage metric:             scripts.Curate_legacy_invocations_per_day
//     (zero for 30 days = safe to delete)
//   - Forbidden compatibility:  see docs/architecture/godlike/
//     07_ZERO_LEGACY_POLICY.md §"7 forbidden
//     techniques" — no silent shims, no
//     re-export aliases, no double-writes.
//
// Once the metric is zero for 30 days, the follow-up PR deletes
// curation_types.go in its entirety + media_curator.go + the
// NewMediaCurator call site in wire_script.go::301 + the
// MediaCurator field on ScriptFlowDeps. Until then, callers
// MUST prefer the canonical SourceCurate + GenerateOneUseCase
// pipeline; the legacy types are visible but discouraged.
package dto

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
	serverURL   string
	clipsRepo   any // *assets.ClipsRepository (avoid import cycle)
	clipBuilder any
	clipSearch  any
	log         *zap.Logger
}

// NewMediaCurator is the canonical constructor for the scriptdto
// MediaCurator (wave-13 owner: internal/capabilities/scripts).
//
// Drift-fix PR (June 2026): the constructor was previously
// inferred removed by a parallel capability refactor — re-introduced
// here as the minimal-scope fix to unblock wire_script.go's
// `mediaCurator = scriptdto.NewMediaCurator(...)` call site, which is
// gated on this symbol existing (the underlying struct fields are
// unexported, so callers cannot construct an instance via `&MediaCurator{}`).
// Field wiring matches the pre-drift shape exactly: serverURL,
// clipsRepo, clipBuilder, log all set; clipSearch is
// late-bound via SetGenerateOneUC / SetClipSearchPort setters (the
// composition root stamps them when those bundles are available).
func NewMediaCurator(serverURL string, clipsRepo any, clipBuilder any, log *zap.Logger) *MediaCurator {
	return &MediaCurator{
		serverURL:   serverURL,
		clipsRepo:   clipsRepo,
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// SetClipSearchPort attaches the optional semantic-search port
// (clipSearch leg). Accepts any so callers can pass any
// concrete ClipSearchPort (qdrant.NewClipSearchAdapter, a
// clipSearchPortAdapter bridge to usecase.ClipSearchPort, or a test
// fake) without forcing MediaCurator to import the typed port
// packages.
func (m *MediaCurator) SetClipSearchPort(port any) {
	if m == nil {
		return
	}
	m.clipSearch = port
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
	// PG-034 (June 2026): HintClipIDs lets callers seed a curation with
	// pre-resolved clip IDs from upstream sources, replacing the deleted
	// Qdrant semantic-search leg.
	HintClipIDs []string
	// PJ-CURATE-1 (June 2026): Search opt-in to the any.
	Search bool
	// PJ-CURATE-1: AllowTextOnly opts back into the legacy
	// text-only fallback when both port and hint list are empty.
	// Defaults to false → ErrCurateNoClips surfaces on empty
	// resolution.
	AllowTextOnly bool
}

// DEPRECATED (PR 4, June 2026): CurateResult was the legacy curate
// output shape. The unified pipeline now routes every curate source
// through SourceCurate + GenerateOneUseCase, returning the canonical
// *scriptpkg.GenerationResult. CurateResult remains as a thin
// backward-compat wrapper for callers that have not yet migrated to
// the unified result shape; new code MUST consume GenerationResult.
//
// Removal tracked in follow-up PR. Wave-13 ownership:
// internal/capabilities/scripts (PR wave owner).
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
