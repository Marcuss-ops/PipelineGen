package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

type testAssetMutator struct {
	db *sql.DB
}

func (m *testAssetMutator) PatchAsset(ctx context.Context, patch persistence.AssetPatch) error {
	if m == nil || m.db == nil {
		return fmt.Errorf("test asset mutator: database is unavailable")
	}
	if patch.MetadataPatchJSON != nil {
		updatedAt := time.Now().UTC().Format(time.RFC3339)
		if patch.UpdatedAt != nil {
			updatedAt = *patch.UpdatedAt
		}
		_, err := m.db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ? WHERE id = ?`, *patch.MetadataPatchJSON, updatedAt, patch.AssetID)
		return err
	}
	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if patch.SearchText != nil {
		sets = append(sets, "search_text = ?")
		args = append(args, *patch.SearchText)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339), patch.AssetID)
	_, err := m.db.ExecContext(ctx, "UPDATE media_assets SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	return err
}
func (m *testAssetMutator) PatchAssetTx(ctx context.Context, tx persistence.Transaction, patch persistence.AssetPatch) error {
	return m.PatchAsset(ctx, patch)
}
func (m *testAssetMutator) ReconcileDriveLocations(context.Context, []persistence.DriveLocationPatch) error {
	return nil
}
func (m *testAssetMutator) ReconcileDriveLocationsTx(context.Context, persistence.Transaction, []persistence.DriveLocationPatch) error {
	return nil
}

var _ persistence.AssetMutator = (*testAssetMutator)(nil)
