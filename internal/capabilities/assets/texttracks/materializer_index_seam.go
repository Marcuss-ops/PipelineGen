// Package texttracks — materializer_index_seam.go: the post-translation index
// seam of the materializer.
//
// POSTGRES-MEDIA-CUTOVER follow-up (September 2026). Split out of
// materializer.go for the godlike/08 max_lines_per_file_strict gate (600);
// the seam is a self-contained unit — two optional ports, the rebuild that
// must precede the reindex, and the legacy emission for compositions with no
// media plane.
//
// The seam owns the answer to "after the tracks are durable, how does what
// search can SEE change?":
//
//  1. search_text is recomposed from the asset's metadata plus every READY
//     transcript in the configured languages, so translated text enters the
//     embedding instead of only the text-track table;
//  2. a reindex is requested on the media index plane — but only when
//     something actually changed, which is what makes a repair run safe to
//     repeat over the whole catalog.
package texttracks

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// SetIndexRequester wires the canonical media index-plane request port
// (production concrete: *pgmedia.ReindexRequester). When set, the
// materializer requests its post-translation reindex through this port
// instead of the operational SQLite outbox. It is safe to call before the
// first Materialize; the materializer is only safe for concurrent
// Materialize calls when the port is set once at composition time.
func (m *Materializer) SetIndexRequester(r IndexRequester) {
	if m == nil {
		return
	}
	m.reindex = r
}

// SetSearchTextRebuilder wires the multilingual search_text rebuilder
// (production concrete: *pgmedia.SearchTextRebuilder). When set, the
// materializer recomposes the asset's search_text from its metadata and
// the READY transcripts before requesting the reindex, so the
// translations become part of the embedded text.
func (m *Materializer) SetSearchTextRebuilder(r SearchTextRebuilder) {
	if m == nil {
		return
	}
	m.searchText = r
}

// refreshIndexInput is the canonical post-translation index seam. It runs
// AFTER the new READY translations are durable and BEFORE any reindex is
// requested:
//
//  1. Rebuild media_assets.search_text so the translations become part of
//     the embedding input. The PostgreSQL index worker embeds search_text
//     verbatim (pgmedia.EmbedAssetTextAdapter); a reindex without this step
//     would re-embed the pre-translation text and the translations would
//     stay invisible to semantic search.
//  2. Request the reindex on the canonical media index plane. When the
//     PostgreSQL IndexRequester port is wired (production) it is the ONLY
//     emission path; the legacy SQLite outbox fallback exists solely for
//     compositions without a media plane and delivers nothing, because no
//     media handler is registered on the SQLite outbox in any mode.
//
// tracksChanged reports whether THIS invocation mutated the asset's
// tracks. It is not the only reindex trigger: a rebuild that changed the
// document is sufficient on its own, which is what lets an operator repair
// an asset whose translations were written long ago but never indexed.
func (m *Materializer) refreshIndexInput(
	ctx context.Context,
	assetID string,
	kind detail.TextTrackKind,
	tracksChanged bool,
	report *MaterializationReport,
) error {
	documentChanged := false
	if m.searchText != nil {
		changed, err := m.searchText.Rebuild(ctx, assetID)
		if err != nil {
			return fmt.Errorf("texttracks.materialize: rebuild search_text: %w", err)
		}
		documentChanged = changed
		report.IndexInputRebuilt = true
		m.log.Info("texttracks.materialize.search_text_rebuilt",
			zap.String("asset_id", assetID),
			zap.Bool("changed", changed),
		)
	}

	if !tracksChanged && !documentChanged {
		report.ReindexSkippedReason = "no track change and search_text unchanged"
		m.log.Info("texttracks.materialize.reindex_skipped",
			zap.String("asset_id", assetID),
			zap.String("reason", report.ReindexSkippedReason),
		)
		return nil
	}

	if m.reindex != nil {
		if err := m.reindex.RequestIndex(ctx, assetID); err != nil {
			return fmt.Errorf("texttracks.materialize: request reindex: %w", err)
		}
		report.ReindexRequested = true
		return nil
	}
	if err := m.emitAssetIndexRequested(ctx, assetID, kind); err != nil {
		return err
	}
	report.ReindexRequested = true
	return nil
}

// emitAssetIndexRequested enqueues the canonical
// asset.index.requested event so the Qdrant reindex pipeline
// picks up the new READY tracks.
func (m *Materializer) emitAssetIndexRequested(
	ctx context.Context,
	assetID string,
	kind detail.TextTrackKind,
) error {
	payload, err := json.Marshal(map[string]string{
		"asset_id": assetID,
		"kind":     string(kind),
		"reason":   "asset.text.materialize complete",
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = m.outbox.Enqueue(
		ctx, nil,
		outboxevents.EventAssetIndexRequested,
		assetID,
		"asset",
		string(payload),
		"",
	)
	return err
}
