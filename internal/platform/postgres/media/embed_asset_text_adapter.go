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

// EmbedAssetTexts is the batch surface of the worker's embedding port. The
// DB leg is N→1 (LoadAssetSearchTexts); the provider leg stays one Embed
// per non-blank text because the sidecar /embed contract is single-text.
// When the sidecar grows a verified batch endpoint, only this method changes
// — the worker's batch path already consumes the map shape.
//
// An asset whose search_text is blank resolves to the embedder's canonical
// empty result (EmbeddingResult{}, nil) and is omitted from the map so the
// worker's per-asset path keeps ownership of the zero-length decision.
func (a *EmbedAssetTextAdapter) EmbedAssetTexts(ctx context.Context, assetIDs []string) (map[string][]float32, error) {
	texts, err := a.LoadAssetSearchTexts(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]float32, len(texts))
	for id, text := range texts {
		res, err := a.embeder.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed asset %q: %w", id, err)
		}
		if len(res.Vector) == 0 {
			continue
		}
		out[id] = res.Vector
	}
	return out, nil
}
