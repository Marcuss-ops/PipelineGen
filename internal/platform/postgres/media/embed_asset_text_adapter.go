// Package media — embed_asset_text_adapter.go: the search_text → embedding
// adapter for the PostgreSQL index worker.
//
// Extracted from outbox_worker.go (2026-09-12) to keep the outbox
// consumption lifecycle under the max_lines_per_file_strict=600
// forward-prevention cap (godlike/08): the worker owns the outbox
// lease loop, this adapter owns the search_text read + embed port
// bridging.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// EmbedAssetTextAdapter adapts the kernel asset.Embedder (HTTPTextEmbedder)
// to the worker's AssetEmbedder port: the asset's search_text is fetched
// from the media SSOT and embedded with the canonical text-channel model.
type EmbedAssetTextAdapter struct {
	db      *sql.DB
	embeder coreasset.Embedder
}

// NewEmbedAssetTextAdapter constructs the adapter. Both deps required.
func NewEmbedAssetTextAdapter(db *sql.DB, embedder coreasset.Embedder) *EmbedAssetTextAdapter {
	if db == nil {
		panic("media.NewEmbedAssetTextAdapter: db is required")
	}
	if embedder == nil {
		panic("media.NewEmbedAssetTextAdapter: embedder is required")
	}
	return &EmbedAssetTextAdapter{db: db, embeder: embedder}
}

// EmbedAssetText reads search_text from media_assets and embeds it.
func (a *EmbedAssetTextAdapter) EmbedAssetText(ctx context.Context, assetID string) ([]float32, error) {
	var text string
	if err := a.db.QueryRowContext(ctx,
		`SELECT search_text FROM media_assets WHERE id = $1`, assetID,
	).Scan(&text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("embed asset %q: asset not found in media SSOT", assetID)
		}
		return nil, fmt.Errorf("embed asset %q: read search_text: %w", assetID, err)
	}
	if strings.TrimSpace(text) == "" {
		// Empty source data cannot be repaired by retrying the provider. Mark it
		// terminal so the outbox does not burn exponential-backoff attempts.
		return nil, event.NewTerminalError(fmt.Errorf("embed asset %q: search_text is empty", assetID))
	}
	res, err := a.embeder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	return res.Vector, nil
}

// LoadAssetSearchTexts reads the canonical search_text for every requested
// asset in ONE round-trip (a parameterised IN list) instead of one SELECT
// per asset. Assets absent from the media SSOT are simply absent from the
// returned map, so the caller decides whether an unresolved ID is fatal.
func (a *EmbedAssetTextAdapter) LoadAssetSearchTexts(ctx context.Context, assetIDs []string) (map[string]string, error) {
	texts := make(map[string]string, len(assetIDs))
	if len(assetIDs) == 0 {
		return texts, nil
	}
	placeholders := make([]string, len(assetIDs))
	args := make([]any, len(assetIDs))
	for i, id := range assetIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, search_text FROM media_assets WHERE id IN (`+strings.Join(placeholders, ", ")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("load search_text for %d asset(s): %w", len(assetIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, fmt.Errorf("scan search_text row: %w", err)
		}
		texts[id] = text
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search_text rows: %w", err)
	}
	return texts, nil
}

// batchTextEmbedder is the optional extension implemented by sidecars that
// provide canonical, order-preserving text batch inference.
type batchTextEmbedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([]coreasset.EmbeddingResult, error)
}

// EmbedAssetTexts resolves the canonical search_text values in one database
// query and, when supported, one E5 batch inference request. The single-text
// interface remains the compatibility fallback for other Embedder providers.
// Blank search_text remains omitted so worker retry/dead-letter behavior does
// not change.
func (a *EmbedAssetTextAdapter) EmbedAssetTexts(ctx context.Context, assetIDs []string) (map[string][]float32, error) {
	texts, err := a.LoadAssetSearchTexts(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	return a.embedSearchTexts(ctx, assetIDs, texts)
}

func (a *EmbedAssetTextAdapter) embedSearchTexts(ctx context.Context, assetIDs []string, texts map[string]string) (map[string][]float32, error) {
	out := make(map[string][]float32, len(texts))
	batch, supportsBatch := a.embeder.(batchTextEmbedder)
	if !supportsBatch {
		seen := make(map[string]struct{}, len(assetIDs))
		for _, id := range assetIDs {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			text, found := texts[id]
			if !found || strings.TrimSpace(text) == "" {
				continue
			}
			res, err := a.embeder.Embed(ctx, text)
			if err != nil {
				return nil, fmt.Errorf("embed asset %q: %w", id, err)
			}
			if len(res.Vector) != 0 {
				out[id] = res.Vector
			}
		}
		return out, nil
	}

	orderedIDs := make([]string, 0, len(texts))
	orderedTexts := make([]string, 0, len(texts))
	seen := make(map[string]struct{}, len(texts))
	for _, id := range assetIDs {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		text, found := texts[id]
		if !found || strings.TrimSpace(text) == "" {
			continue
		}
		orderedIDs = append(orderedIDs, id)
		orderedTexts = append(orderedTexts, text)
	}
	if len(orderedTexts) == 0 {
		return out, nil
	}
	for start := 0; start < len(orderedIDs); start += 32 {
		end := min(start+32, len(orderedIDs))
		results, err := batch.EmbedBatch(ctx, orderedTexts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed assets %q..%q as batch: %w", orderedIDs[start], orderedIDs[end-1], err)
		}
		if len(results) != end-start {
			return nil, fmt.Errorf("text embedder returned %d batch results for %d asset texts", len(results), end-start)
		}
		for i, result := range results {
			if len(result.Vector) != 0 {
				out[orderedIDs[start+i]] = result.Vector
			}
		}
	}
	return out, nil
}
