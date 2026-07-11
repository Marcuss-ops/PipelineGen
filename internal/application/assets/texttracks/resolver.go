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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ResolverConfig is the read-only configuration the resolver
// consumes.
type ResolverConfig struct {
	MaterializeLanguages []string
	SourceLanguage       string
	ModelVersion         string
	PromptVersion        string
	TranslationPolicy    string
	TranslationModel     string
}

// Validate checks the config for mandatory fields.
func (c ResolverConfig) Validate() error {
	if c.SourceLanguage == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "SourceLanguage",
			Reason: "source_language is required",
		}
	}
	if len(c.MaterializeLanguages) == 0 {
		return &ErrInvalidMaterializeRequest{
			Field:  "MaterializeLanguages",
			Reason: "materialize_languages must be non-empty",
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
func (r *Resolver) CandidateLanguages() []string {
	out := make([]string, 0, len(r.cfg.MaterializeLanguages))
	for _, lang := range r.cfg.MaterializeLanguages {
		if lang == r.cfg.SourceLanguage {
			continue
		}
		out = append(out, lang)
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
