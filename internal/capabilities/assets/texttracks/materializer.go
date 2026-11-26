// Package texttracks — materializer.go: TextTrackMaterializer is the
// canonical application-layer service that materializes a media
// asset's text tracks into all configured target languages.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// Pipeline (canonical):
//
//	a. Read source text track READY in asset_text_tracks.
//	b. For each candidate target language, find existing target
//	   track. If READY + matching MaterializationKey → SKIP.
//	c. For missing or stale targets, translate via canonical
//	   TranslationPort (full-text).
//	d. Save the new track as READY with provider/model/version
//	   provenance.
//	e. Emit asset.index.requested outbox event for pgvector reindex.
//	f. On translation error: record in FailedLanguages; loop
//	   continues with the next target language.
package texttracks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	outbox "github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
	"go.uber.org/zap"
)

// OutboxEnqueuer is the narrow port the materializer uses to
// emit the asset.index.requested event.
type OutboxEnqueuer interface {
	Enqueue(
		ctx context.Context,
		tx sqlport.Tx,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
	) (*outbox.EnqueueResult, error)
}

// MaterializationReport is the canonical return value.
type MaterializationReport struct {
	AssetID               string               `json:"asset_id"`
	Kind                  detail.TextTrackKind `json:"kind"`
	SourceLanguage        string               `json:"source_language"`
	SourceTextHash        string               `json:"source_text_hash"`
	CreatedLanguages      []string             `json:"created_languages"`
	SkippedLanguages      []string             `json:"skipped_languages"`
	RetranslatedLanguages []string             `json:"retranslated_languages"`
	FailedLanguages       map[string]string    `json:"failed_languages"`
	Duration              time.Duration        `json:"duration"`

	// IndexInputRebuilt reports that the multilingual search_text repair
	// ran for this asset. It is set whenever a SearchTextRebuilder is
	// wired, INCLUDING runs that created no translation: the operator
	// backfill over the pre-existing catalog always finds the language
	// rows already READY, and repairing their index input is the whole
	// point of re-running it.
	IndexInputRebuilt bool `json:"index_input_rebuilt"`
	// ReindexRequested reports that a reindex was actually asked for, on
	// whichever plane owns media indexing. A run whose rebuild left the
	// document byte-identical asks for nothing, so a repeated backfill
	// over an unchanged asset does not re-embed the same text.
	ReindexRequested bool `json:"reindex_requested"`
	// ReindexSkippedReason is set when the repair ran but no reindex was
	// requested, and names why. It exists so an operator can tell
	// "nothing to repair" apart from "the repair silently did nothing".
	ReindexSkippedReason string `json:"reindex_skipped_reason,omitempty"`
}

func (r *MaterializationReport) HasFailures() bool {
	return len(r.FailedLanguages) > 0
}

// Materializer is the canonical application-layer service.
type Materializer struct {
	repo        detail.TextTrackRepository
	translator  translation.TranslationPort
	outbox      OutboxEnqueuer
	resolverCfg ResolverConfig
	log         *zap.Logger

	// reindex is the canonical media index-plane request port. When set
	// (production: always, whenever the PostgreSQL media plane is enabled)
	// it REPLACES the legacy SQLite outbox emission entirely — the SQLite
	// operational outbox has no media handler in any mode, so an
	// asset.index.requested row written there can only dead-letter.
	reindex IndexRequester

	// searchText recomposes media_assets.search_text from the asset's
	// canonical metadata plus every READY transcript track (original +
	// translations) BEFORE the reindex is requested, so the translations
	// this run just persisted actually enter the E5 embedding. nil keeps
	// the legacy behaviour (rebuild not wired).
	searchText SearchTextRebuilder

	// mu guards the MaterializationReport mutations that concurrent
	// materializeOne goroutines share (CreatedLanguages, SkippedLanguages,
	// RetranslatedLanguages, FailedLanguages).
	mu sync.Mutex
	// concurrency bounds the parallel per-language fan-out. 1 (default)
	// preserves the legacy sequential behavior; >1 runs candidate
	// translations concurrently. The concrete speedup is bounded by the
	// upstream translator's own parallelism (e.g. Ollama
	// OLLAMA_NUM_PARALLEL).
	concurrency int
}

func NewMaterializer(
	repo detail.TextTrackRepository,
	translator translation.TranslationPort,
	outbox OutboxEnqueuer,
	resolverCfg ResolverConfig,
	log *zap.Logger,
) (*Materializer, error) {
	if repo == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: repo is nil")
	}
	if translator == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: translator is nil")
	}
	if outbox == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: outbox is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: log is nil")
	}
	if err := resolverCfg.Validate(); err != nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: %w", err)
	}
	return &Materializer{
		repo:        repo,
		translator:  translator,
		outbox:      outbox,
		resolverCfg: resolverCfg,
		log:         log,
		concurrency: 1,
	}, nil
}

// SetConcurrency bounds the parallel per-language fan-out. Values <1 are
// clamped to 1 (sequential). It is safe to call before Materialize; the
// materializer is safe for concurrent Materialize calls only when a single
// concurrency value is set once at composition time.
func (m *Materializer) SetConcurrency(n int) {
	if m == nil {
		return
	}
	if n < 1 {
		n = 1
	}
	m.concurrency = n
}

// SetIndexRequester / SetSearchTextRebuilder and the post-translation index
// seam they feed live in materializer_index_seam.go (godlike/08 file-size
// split, September 2026).

// refreshIndexInput and emitAssetIndexRequested live in
// materializer_index_seam.go.
