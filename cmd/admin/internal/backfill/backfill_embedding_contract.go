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
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// embeddingContractSource is the narrow media read this backfill depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader (pgmedia.BackfillReader).
// The eligibility boundary is capregistry.SearchIndexTaxonomySQL inside that
// reader, so the backfill grades exactly the rows the indexer projects.
type embeddingContractSource interface {
	CountEmbeddingContractEligible(ctx context.Context, modelID, modelRevision string) (int, error)
	CountEmbeddingContractStamped(ctx context.Context, contractHash string) (int, error)
	ListEmbeddingContractToStamp(ctx context.Context, modelID, modelRevision, contractHash string) ([]string, error)
}

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

	// MEDIA-SSOT: the candidate scan reads media_assets, so it resolves from the
	// PostgreSQL media SSOT rather than the operational mirror, which holds no
	// committed media rows.
	source := pgmedia.NewBackfillReader(root.MediaPostgres)
	if source == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for embedding-contract backfill")
	}

	ctx := context.Background()
	eligible, err := source.CountEmbeddingContractEligible(ctx, coreembedding.CanonicalText.ModelID, coreembedding.CanonicalText.ModelRevision)
	if err != nil {
		return err
	}
	already, err := source.CountEmbeddingContractStamped(ctx, coreembedding.CanonicalText.Hash())
	if err != nil {
		return err
	}
	updated := 0
	if *apply {
		ids, err := source.ListEmbeddingContractToStamp(ctx, coreembedding.CanonicalText.ModelID, coreembedding.CanonicalText.ModelRevision, coreembedding.CanonicalText.Hash())
		if err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		patch, _ := json.Marshal(map[string]string{"embedding_contract_hash": coreembedding.CanonicalText.Hash()})
		for _, assetID := range ids {
			patchJSON := string(patch)
			if err := mutator.PatchAsset(ctx, persistence.AssetPatch{
				AssetID: assetID, MetadataPatchJSON: &patchJSON, UpdatedAt: &now,
			}); err != nil {
				return fmt.Errorf("stamp embedding contract hash for %s: %w", assetID, err)
			}
			updated++
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
