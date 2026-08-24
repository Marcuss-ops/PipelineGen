package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// runBackfillEmbeddingContract stamps the canonical contract hash only on
// rows whose observed model and revision already match the E5 contract. It
// never upgrades an unknown vector by assertion; the vector must first be
// produced by the canonical indexer. Dry-run is the default.
func runBackfillEmbeddingContract(args []string) error {
	fs := flag.NewFlagSet("backfill-embedding-contract", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the canonical contract hash")
	jsonOutput := fs.Bool("json", false, "emit a JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	db, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media database: %w", err)
	}
	defer db.Close()

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
		result, err := db.ExecContext(ctx, `UPDATE media_assets SET metadata_json=json_set(COALESCE(metadata_json,'{}'),'$.embedding_contract_hash',?) WHERE `+searchable+` AND COALESCE(json_extract(metadata_json,'$.embedding_model'),'')=? AND COALESCE(json_extract(metadata_json,'$.embedding_model_version'),'')=? AND COALESCE(json_extract(metadata_json,'$.embedding_contract_hash'),'') != ?`, coreembedding.CanonicalText.Hash(), coreembedding.CanonicalText.ModelID, coreembedding.CanonicalText.ModelRevision, coreembedding.CanonicalText.Hash())
		if err != nil {
			return fmt.Errorf("stamp embedding contract hash: %w", err)
		}
		updated64, _ := result.RowsAffected()
		updated = int(updated64)
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
