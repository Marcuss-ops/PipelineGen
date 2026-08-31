package backfill

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
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	addString := func(column string, value *string) {
		if value != nil {
			sets = append(sets, column+" = ?")
			args = append(args, *value)
		}
	}
	addString("search_text", patch.SearchText)
	addString("folder_id", patch.FolderID)
	addString("folder_path", patch.FolderPath)
	addString("lifecycle_state", patch.LifecycleState)
	addString("deleted_at", patch.DeletedAt)
	if patch.MetadataPatchJSON != nil {
		updatedAt := time.Now().UTC().Format(time.RFC3339)
		if patch.UpdatedAt != nil {
			updatedAt = *patch.UpdatedAt
		}
		args = append(args, *patch.MetadataPatchJSON, updatedAt, patch.AssetID)
		_, err := m.db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ? WHERE id = ?`, args...)
		return err
	}
	if len(sets) == 0 {
		return nil
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if patch.UpdatedAt != nil {
		updatedAt = *patch.UpdatedAt
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, updatedAt, patch.AssetID)
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
