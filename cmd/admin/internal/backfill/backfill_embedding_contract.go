package backfill

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// runBackfillEmbeddingContract stamps the canonical contract hash only on
// rows whose observed model and revision already match the E5 contract. It
// never upgrades an unknown vector by assertion; the vector must first be
// produced by the canonical indexer. Dry-run is the default.
func RunBackfillEmbeddingContract(args []string) error {
	fs := flag.NewFlagSet("backfill-embedding-contract", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the canonical contract hash")
	jsonOutput := fs.Bool("json", false, "emit a JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database is required")
	}
	mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
	if !ok || mutator == nil {
		return fmt.Errorf("canonical asset mutation committer is not available")
	}
	db := root.DB.DB

	ctx := context.Background()
	const searchable = capregistry.SearchIndexTaxonomySQL + ` AND lifecycle_state IN ('ACTIVE','PUBLISHED')`
	var eligible, already, updated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE `+searchable+` AND COALESCE(json_extract(metadata_json,'$.embedding_model'),'')=? AND COALESCE(json_extract(metadata_json,'$.embedding_model_version'),'')=?`, coreembedding.CanonicalText.ModelID, coreembedding.CanonicalText.ModelRevision).Scan(&eligible); err != nil {
		return fmt.Errorf("count eligible embeddings: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE `+searchable+` AND COALESCE(json_extract(metadata_json,'$.embedding_contract_hash'),'')=?`, coreembedding.CanonicalText.Hash()).Scan(&already); err != nil {
		return fmt.Errorf("count stamped embeddings: %w", err)
	}
	if *apply {
		rows, err := db.QueryContext(ctx, `SELECT id FROM media_assets WHERE `+searchable+` AND COALESCE(json_extract(metadata_json,'$.embedding_model'),'')=? AND COALESCE(json_extract(metadata_json,'$.embedding_model_version'),'')=? AND COALESCE(json_extract(metadata_json,'$.embedding_contract_hash'),'') != ? ORDER BY id`, coreembedding.CanonicalText.ModelID, coreembedding.CanonicalText.ModelRevision, coreembedding.CanonicalText.Hash())
		if err != nil {
			return fmt.Errorf("find embeddings to stamp: %w", err)
		}
		defer rows.Close()
		now := time.Now().UTC().Format(time.RFC3339)
		patch, _ := json.Marshal(map[string]string{"embedding_contract_hash": coreembedding.CanonicalText.Hash()})
		for rows.Next() {
			var assetID string
			if err := rows.Scan(&assetID); err != nil {
				return fmt.Errorf("scan embedding to stamp: %w", err)
			}
			patchJSON := string(patch)
			if err := mutator.PatchAsset(ctx, persistence.AssetPatch{
				AssetID: assetID, MetadataPatchJSON: &patchJSON, UpdatedAt: &now,
			}); err != nil {
				return fmt.Errorf("stamp embedding contract hash for %s: %w", assetID, err)
			}
			updated++
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate embeddings to stamp: %w", err)
		}
	}
	report := map[string]any{"mode": "dry-run", "eligible_observed_e5": eligible, "already_stamped": already, "updated": updated, "contract_hash": coreembedding.CanonicalText.Hash()}
	if *apply {
		report["mode"] = "apply"
	}
	if *jsonOutput {
		encoded, _ := json.Marshal(report)
		fmt.Println(string(encoded))
		return nil
	}
	fmt.Printf("Embedding contract backfill: mode=%s eligible=%d already=%d updated=%d\n", report["mode"], eligible, already, updated)
	return nil
}
