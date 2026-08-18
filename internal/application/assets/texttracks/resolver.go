// Package texttracks — resolver.go: source-track lookup + candidate
// language selection helpers for TextTrackMaterializer.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "where is the source track for this (asset, kind)?" +
// "which target languages should we materialize?" decisions.
package texttracks

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ResolverConfig is the read-only configuration the resolver
// consumes.
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026): the legacy
// `MaterializeLanguages []string` field is REMOVED. The
// canonical pipeline-language set now flows through
// `Registry asset.LanguageRegistry` (godlike/06 SSOT — the
// resolver queries `Registry.EnabledLanguages()` filtered by
// `TranslateClips=true`). Per-call candidate overrides
// (Materializer's 6th argument) flow through
// `OverrideTargetLanguages []string` and unconditionally
// bypass the registry filter (godlike/07 fail-closed — the
// caller is responsible for ensuring every override code is
// one the pipeline can translate; the resolver does NOT
// validate against the registry here, because backfill
// operations legitimately need to drill into codes the
// operator has since removed from MaterializeLanguages).
type ResolverConfig struct {
	// Registry is the canonical pipeline-language SSOT.
	// Constructed from cfg.Media.Multilingual.Languages via
	// asset.NewLanguageRegistry in the composition root.
	// nil → pipeline runs in disabled mode (zero candidates
	//           from the registry); the override path is the
	//           only way to inject candidates.
	Registry asset.LanguageRegistry

	// OverrideTargetLanguages is a per-call candidate
	// override. Non-empty → the resolver uses ONLY this
	// list (filtered by source language); used by the
	// backfill CLI + admin jobs to drill into a single
	// language without editing cfg.
	OverrideTargetLanguages []string

	SourceLanguage    string
	ModelVersion      string
	PromptVersion     string
	TranslationPolicy string
	TranslationModel  string
	// OllamaModel is the concrete Ollama model passed to the
	// translation provider's ModelPolicy. Decoupled from
	// TranslationModel so the request fingerprint
	// (TranslationModel) can identify the active translation
	// stack (e.g. "argos-translate") WITHOUT leaking a provider
	// name into the Ollama fallback's model selection.
	// Empty → the provider picks its server default.
	OllamaModel string
}

// Validate checks the config for mandatory fields.
//
// godlike/07 fail-closed: SourceLanguage is required AND
// at least one of (Registry, OverrideTargetLanguages) must
// be populated. OverrideTargetLanguages entries MUST be
// non-empty + non-whitespace BCP-47 codes — an empty
// entry would otherwise fall through to the translator
// port with a blank target_lang and surface a quieter
// error from the LLM instead of the typed sentinel here.
//
// A resolver constructed with neither is a deployment bug,
// not a fallback to "en".
func (c ResolverConfig) Validate() error {
	if c.SourceLanguage == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "SourceLanguage",
			Reason: "source_language is required",
		}
	}
	if c.Registry == nil && len(c.OverrideTargetLanguages) == 0 {
		return &ErrInvalidMaterializeRequest{
			Field:  "Registry",
			Reason: "either LanguageRegistry or OverrideTargetLanguages must be populated",
		}
	}
	for i, code := range c.OverrideTargetLanguages {
		if strings.TrimSpace(code) == "" {
			return &ErrInvalidMaterializeRequest{
				Field:  "OverrideTargetLanguages",
				Reason: fmt.Sprintf("entry at index %d is empty or whitespace", i),
			}
		}
	}
	return nil
}

// Resolver is the source-track + candidate-language selector.
type Resolver struct {
	cfg      ResolverConfig
	repo     asset.TextTrackRepository
	sourceID string
	kind     asset.TextTrackKind
}

func NewResolver(cfg ResolverConfig, repo asset.TextTrackRepository, assetID string, kind asset.TextTrackKind) *Resolver {
	return &Resolver{
		cfg:      cfg,
		repo:     repo,
		sourceID: assetID,
		kind:     kind,
	}
}

// FindSourceTrack returns the canonical READY source track.
func (r *Resolver) FindSourceTrack(ctx context.Context) (*asset.TextTrack, error) {
	track, err := r.repo.Find(ctx, r.sourceID, r.cfg.SourceLanguage, r.kind)
	if err != nil {
		return nil, fmt.Errorf("texttracks.resolver.FindSourceTrack(%s, %s, %s): %w",
			r.sourceID, r.cfg.SourceLanguage, r.kind, err)
	}
	if track == nil {
		return nil, &ErrNoSourceTrack{
			AssetID:        r.sourceID,
			SourceLanguage: r.cfg.SourceLanguage,
			TextKind:       r.kind,
		}
	}
	if track.Status != asset.TextTrackReady {
		// Surface the union of available languages for the
		// same (asset, kind) so the operator dashboard can
		// see "what's actually READY" without a second
		// round-trip. Errors from ListReadyLanguages are
		// non-fatal (the operator falls back to the empty
		// list).
		langs, _ := r.repo.ListReadyLanguages(ctx, r.sourceID, r.kind)
		return nil, &ErrTrackNotReady{
			AssetID:            r.sourceID,
			SourceLanguage:     r.cfg.SourceLanguage,
			TextKind:           r.kind,
			CurrentStatus:      track.Status,
			AvailableStatuses:  []asset.TextTrackStatus{track.Status},
			AvailableLanguages: langs,
		}
	}
	return track, nil
}

// CandidateLanguages returns the canonical target-language set,
// filtered against the source language.
//
// PR-CATALOG-MULTILINGUA step 3 (July 2026): the candidate
// pool is sourced from ONE of two paths, in priority order:
//
//  1. OverrideTargetLanguages (per-call operator override,
//     skips registry filter — backfill drill-down).
//  2. Registry.EnabledLanguages() filtered by
//     TranslateClips=true (godlike/06 SSOT — every pipeline
//     asks the registry for "what languages can I
//     translate into?").
//
// The source language is excluded from the result, matching
// the pre-step-3 semantics (translating from en → en is a
// no-op).
func (r *Resolver) CandidateLanguages() []string {
	var pool []string
	switch {
	case len(r.cfg.OverrideTargetLanguages) > 0:
		// (1) Operator override path. The caller is
		// responsible for ensuring every override code is
		// one the pipeline can actually translate; the
		// resolver does NOT validate against the registry.
		pool = r.cfg.OverrideTargetLanguages
	case r.cfg.Registry != nil:
		// (2) Registry SSOT path. Filter by TranslateClips
		// so TTS-only codes are not fanned out for clip
		// translation.
		for _, s := range r.cfg.Registry.EnabledLanguages() {
			if s.TranslateClips {
				pool = append(pool, s.Code)
			}
		}
	}
	out := make([]string, 0, len(pool))
	for _, l := range pool {
		if l != r.cfg.SourceLanguage {
			out = append(out, l)
		}
	}
	return out
}

// FindExistingTarget returns the existing text track for the
// (asset, target_lang, kind) triple, regardless of status.
func (r *Resolver) FindExistingTarget(ctx context.Context, targetLang string) (*asset.TextTrack, error) {
	track, err := r.repo.Find(ctx, r.sourceID, targetLang, r.kind)
	if err != nil {
		return nil, fmt.Errorf("texttracks.resolver.FindExistingTarget(%s, %s, %s): %w",
			r.sourceID, targetLang, r.kind, err)
	}
	return track, nil
}
