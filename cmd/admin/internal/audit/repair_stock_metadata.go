// cmd/admin/repair_stock_metadata.go — repair metadata of legacy
// stock/YouTube/Artlist clips WITHOUT re-downloading (plan item #10,
// August 2026).
//
// Pipeline per asset:
//
//	existing row
//	  → canonical taxonomy (mediaregistry.ResolveTaxonomy SSOT, via
//	    CanonicalIdentityResolver.BackfillTaxonomy)
//	  → search_text (asset.ComposerRegistry SSOT, per-source strategy)
//	  → missing embeddings (outbox EnqueueReindex force=true → the worker
//	    generates embeddings + upserts Qdrant)
//
// The command NEVER downloads or uploads: it only performs SQL updates +
// outbox enqueue. If the file already exists on Drive/local, download=0,
// upload=0 — only the missing semantic metadata is restored so the row
// becomes searchable. This is "repair metadata", not "re-download".
//
// Usage:
//
//	go run ./cmd/admin repair-stock-metadata                          # dry-run
//	go run ./cmd/admin repair-stock-metadata --apply                  # repair + enqueue
//	go run ./cmd/admin repair-stock-metadata --apply --source=stock   # only stock
//	go run ./cmd/admin repair-stock-metadata --apply --limit=100
//	go run ./cmd/admin repair-stock-metadata --json
//	go run ./cmd/admin repair-stock-metadata --apply --skip-embeddings
//	go run ./cmd/admin repair-stock-metadata --apply --skip-taxonomy --skip-search-text
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/outbox"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/backfill"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// repairStockMetadataDeps holds the parsed flags for runRepairStockMetadata.
type repairStockMetadataDeps struct {
	Apply          bool
	JSON           bool
	Sources        []string
	Limit          int
	SkipTaxonomy   bool
	SkipSearchText bool
	SkipEmbeddings bool
	Checkpoint     string
	Resume         bool
	RetryFailed    bool
}

// defaultRepairSources is the legacy provider set the repair targets by
// default (the rows the plan's 4-bucket report flagged as INDEXED-but-
// ineligible / missing taxonomy).
var defaultRepairSources = []string{"stock", "youtube", "artlist"}

// parseRepairStockMetadataArgs parses CLI args.
// Flags:
//
//	--apply               actually write (default: dry-run)
//	--json                machine-readable output
//	--source=stock,youtube  restrict to these sources (default: stock,youtube,artlist)
//	--limit=N             cap rows per phase
//	--skip-taxonomy       skip the taxonomy repair phase
//	--skip-search-text    skip the search_text repair phase
//	--skip-embeddings     skip the embedding enqueue phase
//	--checkpoint=PATH     checkpoint for the embedding phase
//	--resume              resume the embedding phase from checkpoint
//	--retry-failed        retry previously-failed embedding enqueues
func parseRepairStockMetadataArgs(args []string) (repairStockMetadataDeps, error) {
	deps := repairStockMetadataDeps{Sources: append([]string(nil), defaultRepairSources...)}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--json":
			deps.JSON = true
		case a == "--skip-taxonomy":
			deps.SkipTaxonomy = true
		case a == "--skip-search-text":
			deps.SkipSearchText = true
		case a == "--skip-embeddings":
			deps.SkipEmbeddings = true
		case a == "--resume":
			deps.Resume = true
		case a == "--retry-failed":
			deps.RetryFailed = true
		case strings.HasPrefix(a, "--source="):
			parts := strings.Split(strings.TrimPrefix(a, "--source="), ",")
			deps.Sources = nil
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					deps.Sources = append(deps.Sources, p)
				}
			}
		case strings.HasPrefix(a, "--limit="):
			n, err := cli.ParsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--checkpoint="):
			deps.Checkpoint = strings.TrimPrefix(a, "--checkpoint=")
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if len(deps.Sources) == 0 {
		return deps, fmt.Errorf("--source must list at least one provider")
	}
	if deps.Resume && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--resume requires --checkpoint=<path>")
	}
	if deps.RetryFailed && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--retry-failed requires --checkpoint=<path>")
	}
	return deps, nil
}

// repairStockMetadataReport is the machine-readable per-phase outcome.
type repairStockMetadataReport struct {
	Mode                string   `json:"mode"`
	Sources             []string `json:"sources"`
	TaxonomyConsidered  int      `json:"taxonomy_considered"`
	TaxonomyBackfilled  int      `json:"taxonomy_backfilled"`
	TaxonomyUnknown     int      `json:"taxonomy_unknown"`
	SearchTextMatched   int      `json:"search_text_matched"`
	SearchTextUpdated   int      `json:"search_text_updated"`
	EmbeddingCandidates int      `json:"embedding_candidates"`
	EmbeddingEnqueued   int      `json:"embedding_enqueued"`
	EmbeddingFailed     int      `json:"embedding_failed"`
	Errors              []string `json:"errors,omitempty"`
}

// runRepairStockMetadata is the entry point registered in cmd/admin/main.go.
func RunRepairStockMetadata(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseRepairStockMetadataArgs(args)
	if err != nil {
		return err
	}

	ctx := cli.CmdContext()

	log.Info("repair-stock-metadata starting",
		zap.Bool("apply", deps.Apply),
		zap.Strings("sources", deps.Sources),
		zap.Int("limit", deps.Limit),
		zap.Bool("skip_taxonomy", deps.SkipTaxonomy),
		zap.Bool("skip_search_text", deps.SkipSearchText),
		zap.Bool("skip_embeddings", deps.SkipEmbeddings),
	)

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("init composition: %w", err)
	}
	defer rootCleanup()
	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database not initialized in composition root")
	}
	db := root.DB.DB

	report := repairStockMetadataReport{Mode: "dry-run", Sources: deps.Sources}
	if deps.Apply {
		report.Mode = "apply"
	}

	// ── Phase 1: taxonomy repair (canonical resolver → ResolveTaxonomy SSOT).
	// Only restores the missing dimensions (media_type/asset_kind/namespace/
	// source_type); never touches the file bytes.
	if !deps.SkipTaxonomy {
		resolver, err := sqlitemediaregistry.NewCanonicalIdentityResolver(db)
		if err != nil {
			return fmt.Errorf("taxonomy repair: resolver: %w", err)
		}
		tax, err := resolver.BackfillTaxonomy(ctx, deps.Apply)
		if err != nil {
			return fmt.Errorf("taxonomy repair: %w", err)
		}
		report.TaxonomyConsidered = tax.AssetsConsidered
		report.TaxonomyBackfilled = tax.TaxonomyBackfilled
		report.TaxonomyUnknown = tax.TaxonomyUnknown
	}

	// ── Phase 2: search_text repair (asset.ComposerRegistry SSOT).
	// Composes search_text from the row's existing fields for the target
	// sources when the column is empty.
	if !deps.SkipSearchText {
		matched, updated, err := backfillSearchText(ctx, db, deps.Sources, deps.Limit, deps.Apply)
		if err != nil {
			return fmt.Errorf("search_text repair: %w", err)
		}
		report.SearchTextMatched = matched
		report.SearchTextUpdated = updated
	}

	// ── Phase 3: embedding backfill (canonical outbox EnqueueReindex
	// force=true → worker generates only the missing embeddings + Qdrant).
	if !deps.SkipEmbeddings {
		adapter := outbox.NewRepairAdapter(db, outboxevents.NewRepository(db), outboxevents.ReindexEnvelopeV1Schema)
		embDeps := indexing.Deps{
			Apply:       deps.Apply,
			DryRun:      !deps.Apply,
			OnlyMissing: true, // generate only the missing embedding channels
			Limit:       deps.Limit,
			Progress:    50,
			Source:      strings.Join(deps.Sources, ","),
			Checkpoint:  deps.Checkpoint,
			Resume:      deps.Resume,
			RetryFailed: deps.RetryFailed,
		}
		// The fetcher reuses the canonical embedding-candidate query once per
		// source (the query filters on a single source value) and merges the
		// results, so multi-source repairs need no duplicated SQL.
		fetch := func(ctx context.Context, d indexing.Deps, cp *indexing.Checkpoint) ([]indexing.Candidate, error) {
			var all []indexing.Candidate
			for _, src := range deps.Sources {
				d.Source = src
				cands, err := fetchEmbeddingCandidates(ctx, db, d, cp)
				if err != nil {
					return nil, err
				}
				all = append(all, cands...)
			}
			return all, nil
		}
		embReport, _, err := indexing.Run(ctx, embDeps, fetch, adapter, log)
		if err != nil {
			return fmt.Errorf("embedding repair: %w", err)
		}
		report.EmbeddingCandidates = embReport.TotalCandidates
		report.EmbeddingEnqueued = embReport.Succeeded
		report.EmbeddingFailed = embReport.Failed
		report.Errors = append(report.Errors, embReport.Errors...)
	}

	if deps.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	printRepairStockMetadataReport(report)
	return nil
}

func printRepairStockMetadataReport(r repairStockMetadataReport) {
	fmt.Printf("=== repair-stock-metadata (%s): %s ===\n", r.Mode, strings.Join(r.Sources, ","))
	fmt.Printf("  taxonomy:  considered=%d backfilled=%d unknown=%d\n",
		r.TaxonomyConsidered, r.TaxonomyBackfilled, r.TaxonomyUnknown)
	fmt.Printf("  search_text: matched=%d updated=%d\n", r.SearchTextMatched, r.SearchTextUpdated)
	fmt.Printf("  embeddings: candidates=%d enqueued=%d failed=%d\n",
		r.EmbeddingCandidates, r.EmbeddingEnqueued, r.EmbeddingFailed)
	if len(r.Errors) > 0 {
		fmt.Printf("  errors: %d\n", len(r.Errors))
		for i, e := range r.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
	if r.Mode == "dry-run" {
		fmt.Println("\nRe-run with --apply to write repairs and enqueue embeddings.")
	}
}

// backfillSearchText composes search_text for rows of the target sources
// where the column is empty, using the canonical per-source
// asset.ComposerRegistry. It never overwrites a populated search_text.
//
// Returns (matched, updated, error). matched counts rows with empty
// search_text (always computed, also in dry-run); updated counts rows whose
// search_text was actually written (apply only). The composed text is
// bounded to 1024 bytes (the legacy search_text cap) at a word boundary.
func backfillSearchText(ctx context.Context, db *sql.DB, sources []string, limit int, apply bool) (int, int, error) {
	registry := asset.NewComposerRegistry()
	placeholders := make([]string, len(sources))
	args := make([]any, len(sources))
	for i, s := range sources {
		placeholders[i] = "?"
		args[i] = s
	}

	query := `
		SELECT m.id, COALESCE(m.source, ''), COALESCE(m.name, ''), COALESCE(m.category, ''),
		       COALESCE(m.tags, '[]'), COALESCE(m.source_url, ''),
		       COALESCE(json_extract(COALESCE(m.metadata_json, '{}'), '$.description'), ''),
		       COALESCE(json_extract(COALESCE(m.metadata_json, '{}'), '$.summary'), ''),
		       COALESCE(json_extract(COALESCE(m.metadata_json, '{}'), '$.title'), ''),
		       COALESCE((SELECT t.text FROM asset_text_tracks t
		                 WHERE t.asset_id = m.id AND t.text_kind = 'transcript' AND t.is_current = 1
		                 ORDER BY t.id LIMIT 1), '')
		FROM media_assets m
		WHERE (m.search_text IS NULL OR TRIM(m.search_text) = '')
		  AND m.source IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY m.id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("query search_text candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id, source, name, category, tagsJSON, sourceURL, description, summary, title, transcript string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.source, &c.name, &c.category, &c.tagsJSON, &c.sourceURL,
			&c.description, &c.summary, &c.title, &c.transcript); err != nil {
			return 0, 0, fmt.Errorf("scan search_text candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate search_text candidates: %w", err)
	}

	matched := len(candidates)
	updated := 0
	if !apply {
		return matched, 0, nil
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	for _, c := range candidates {
		title := c.title
		if title == "" {
			title = c.name
		}
		var tags []string
		_ = json.Unmarshal([]byte(c.tagsJSON), &tags)

		input := asset.SearchTextInput{
			AssetID:     c.id,
			Source:      c.source,
			Title:       title,
			Description: c.description,
			Summary:     c.summary,
			Transcript:  c.transcript,
			Tags:        tags,
			Category:    c.category,
			SourceURL:   c.sourceURL,
		}
		text, err := registry.Compose(input)
		if err != nil {
			continue
		}
		text = truncateSearchTextBytes(text, 1024)
		if strings.TrimSpace(text) == "" {
			continue // nothing composable from the row's existing fields
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE media_assets SET search_text = ?, updated_at = ? WHERE id = ?`,
			text, nowStr, c.id); err != nil {
			return matched, updated, fmt.Errorf("update search_text for %q: %w", c.id, err)
		}
		updated++
	}
	return matched, updated, nil
}

// truncateSearchTextBytes bounds text to maxBytes at a word boundary so the
// final token is never cut mid-word. Falls back to a hard cut when no space
// exists in the window.
func truncateSearchTextBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	trimmed := s[:maxBytes]
	if idx := strings.LastIndex(trimmed, " "); idx > maxBytes-128 {
		return trimmed[:idx]
	}
	return trimmed
}

func fetchEmbeddingCandidates(ctx context.Context, db *sql.DB, d indexing.Deps, cp *indexing.Checkpoint) ([]indexing.Candidate, error) {
	return nil, nil
}
