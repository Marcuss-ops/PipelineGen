// asset_job_status.go — the read side of the execution ledger.
//
// The ledger was write-only (RecordJob/RelateAsset/...), so a consumer that
// needed to answer "is the job producing this asset still running?" had no
// query to call. The clip download handler is exactly that consumer: when an
// asset's video is not materialized yet it must distinguish "not available
// yet" (409, retry) from "does not exist" (404), and the honest answer comes
// from the job that is still working on it.
//
// This reads the two canonical tables only:
//   - job_asset_relations (job_id, asset_id, relation, ...) — written by
//     RelateAsset as the job works;
//   - jobs (status, retry_count, created_at) — the job's own lifecycle row.
package jobregistry

import (
	"context"
	"strings"
)

// LatestAssetJobStatus returns the status and retry count of the most recently
// created job related to assetID, and whether any such job exists.
//
// found=false is the fail-closed answer: an empty asset id, a missing
// relation, or a database error all report "no job", never a fabricated
// status. The caller then falls back to its own "asset exists but the artifact
// is missing" answer instead of inventing a state.
func (r *Registry) LatestAssetJobStatus(ctx context.Context, assetID string) (status string, retryCount int, found bool) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" || r == nil || r.db == nil {
		return "", 0, false
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT j.status, COALESCE(j.retry_count, 0)
		  FROM job_asset_relations rel
		  JOIN jobs j ON j.id = rel.job_id
		 WHERE rel.asset_id = ?
		 ORDER BY j.created_at DESC, j.id DESC
		 LIMIT 1`, assetID)
	if err := row.Scan(&status, &retryCount); err != nil {
		return "", 0, false
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return "", 0, false
	}
	return status, retryCount, true
}
