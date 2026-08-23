// client_dr.go — QDRANT-005C PR3 Client method extensions.
//
// Extends qdrant.Client with the REST surface for /snapshots plus
// the merge-payload helper consumed by the reaper.
//
// The snapshot marker doc previously lived in client_snapshots.go
// (Phase 5 consolidation, June 2026). RC reference (Qdrant spec):
//
//	POST   /collections/{n}/snapshots              → CreateSnapshot
//	GET    /collections/{n}/snapshots              → ListSnapshots
//	GET    /collections/{n}/snapshots/{name}       → GetSnapshotURL
//	DELETE /collections/{n}/snapshots/{name}       → DeleteSnapshot
//	PUT    /collections/{n}/snapshots/recover      → RestoreSnapshot
//
// RC references (Qdrant spec):
//
//	POST   /collections/{n}/snapshots              → CreateSnapshot
//	GET    /collections/{n}/snapshots              → ListSnapshots
//	GET    /collections/{n}/snapshots/{name}       → GetSnapshotURL
//	DELETE /collections/{n}/snapshots/{name}       → DeleteSnapshot
//	PUT    /collections/{n}/snapshots/recover      → RestoreSnapshot
//	POST   /collections/{n}/points/payload?wait=true → OverwritePayload (merge=true)
//
// Scope notation (June 2026): the first 5 methods above are PR3 deliverable
// (DR/snapshots). OverwritePayload is **PR3 scope-adjacent** — it ships here
// as a compile-unblocker for the reaper fork landed in commit 07292503, NOT
// as a designed surface of the DR feature. It is functional code, not a
// stub, because the reaper was calling a method that didn't exist; if a
// future reaper redesign wants a different backbone, OverwritePayload can
// be deleted independently.
//
// QDRANT-005C PR3 invariants (June 2026):
//   - CreateSnapshot is idempotent within Qdrant: repeated POST with same
//     collection reuses the same snapshot on disk, returns the same Name.
//   - RestoreSnapshot's destination collection is REPLACED in place —
//     the Qdrant REST docs warn that this is destructive. The caller
//     (dr.RestoreService) MUST run ReindexVerifier.VerifyReindex BEFORE
//     flipping the runtime alias to the restored collection.  This
//     closes the silent-partial-restore outage class.
//   - OverwritePayload uses merge=true so a partial-payload call LEAVES
//     the canonical point vector intact. This was the upstream fix
//     in commit 07292503 — previously UpsertPoints had been used, which
//     nulled vectors on partial payload and corrupted the index.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// CreateSnapshot triggers a snapshot of the given collection. Returns
// the descriptor with name + size + checksum + creation time. The
// download URL is NOT embedded — call GetSnapshotURL on the resulting
// name to resolve it.
//
// Idempotency: Qdrant may return the same Name on repeated POSTs of the
// same collection. Treat this as the source-of-truth for subsequent
// List/Restore.
func (c *Client) CreateSnapshot(ctx context.Context, collection string) (*schema.SnapshotDescription, error) {
	if err := c.requireSchemaMatch(collection); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/collections/%s/snapshots", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create snapshot %q: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}
	var rpc struct {
		Result schema.SnapshotDescription `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return nil, fmt.Errorf("decode create snapshot: %w", err)
	}
	c.log.Info("snapshot created", zap.String("collection", collection), zap.String("name", rpc.Result.Name), zap.Int64("size", rpc.Result.Size))
	return &rpc.Result, nil
}

// ListSnapshots returns all snapshots for the supplied collection.
func (c *Client) ListSnapshots(ctx context.Context, collection string) ([]schema.SnapshotDescription, error) {
	if err := c.requireSchemaMatch(collection); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/collections/%s/snapshots", c.baseURL, collection)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("list snapshots %q: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}
	var rpc struct {
		Result []schema.SnapshotDescription `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return nil, fmt.Errorf("decode list snapshots: %w", err)
	}
	return rpc.Result, nil
}

// GetSnapshotURL resolves the snapshot's download URL. The Qdrant REST
// surface splits list/create from URL-get so that listing is cheap
// (does not allocate a download URL per snapshot). Operators invoke
// the URL get lazily — and dr.RestoreService is the canonical caller.
func (c *Client) GetSnapshotURL(ctx context.Context, collection, snapshotName string) (string, error) {
	if snapshotName == "" {
		return "", fmt.Errorf("qdrant.Client.GetSnapshotURL: snapshotName must not be empty")
	}
	url := fmt.Sprintf("%s/collections/%s/snapshots/%s", c.baseURL, collection, snapshotName)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("get snapshot URL %q in %q: %w", snapshotName, collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("qdrant.Client.GetSnapshotURL: snapshot %q not found in %q", snapshotName, collection)
	}
	if resp.StatusCode != http.StatusOK {
		return "", c.parseError(resp)
	}
	var rpc struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", fmt.Errorf("decode snapshot URL: %w", err)
	}
	if rpc.Result == "" {
		return "", fmt.Errorf("qdrant.Client.GetSnapshotURL: empty URL returned for snapshot %q in %q", snapshotName, collection)
	}
	return rpc.Result, nil
}

// DeleteSnapshot removes a snapshot. Idempotent: a not-found snapshot
// is treated as success (mirrors DeleteCollection's pattern).
//
// Restoration-restoration safety: deleting a snapshot that dr.RestoreService
// is actively restoring is racy. caller is responsible for the lock —
// the admin CLI runs single-threaded per process, so this is fine in
// practice. A future improvement would be to add a "delete-if-not-restoring"
// pre-check.
func (c *Client) DeleteSnapshot(ctx context.Context, collection, snapshotName string) error {
	if snapshotName == "" {
		return fmt.Errorf("qdrant.Client.DeleteSnapshot: snapshotName must not be empty")
	}
	url := fmt.Sprintf("%s/collections/%s/snapshots/%s", c.baseURL, collection, snapshotName)
	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("delete snapshot %q from %q: %w", snapshotName, collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone — idempotent
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	c.log.Info("snapshot deleted", zap.String("collection", collection), zap.String("name", snapshotName))
	return nil
}

// RestoreSnapshot rehydrates the destination collection from a
// snapshot URL. The Qdrant REST /snapshots/recover endpoint downloads
// the snapshot from the supplied URL and replaces the destination
// collection's data with it.
//
// CRITICAL (QDRANT-005C, June 2026): the destination collection is
// DESTROYED and re-created with the snapshot's data. CALLER MUST run
// ReindexVerifier.VerifyReindex on the destination BEFORE switching
// the runtime alias to it. This is the canonical verify-then-switch
// pattern; bypassing the verify is the silent partial-restore outage
// class. dr.RestoreService owns this contract — production callers
// go through dr.RestoreService, not this method directly.
func (c *Client) RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error {
	if collection == "" {
		return fmt.Errorf("qdrant.Client.RestoreSnapshot: collection must not be empty")
	}
	if snapshotURL == "" {
		return fmt.Errorf("qdrant.Client.RestoreSnapshot: snapshotURL must not be empty")
	}
	body := map[string]any{
		"location": snapshotURL,
	}
	url := fmt.Sprintf("%s/collections/%s/snapshots/recover", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("restore snapshot to %q from %q: %w", collection, snapshotURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	c.log.Info("snapshot restored", zap.String("collection", collection), zap.String("url", snapshotURL))
	return nil
}

// OverwritePayload applies a per-point selective payload merge via
// Qdrant REST POST /points/payload with merge=true. Vectors are NOT
// affected — only the supplied payload fields overwrite existing
// values (or insert new ones). Required by reaper.Reaper.RedactPayload
// to strip payload keys without mutating the canonical point vectors.
//
// QDRANT-005 (June 2026) — the previous reaper implementation used
// UpsertPoints and accidentally nulled vectors on partial payload;
// this method is the upstream fix shipped with commit 07292503's
// reaper path. Empty payload list is a no-op.
func (c *Client) OverwritePayload(ctx context.Context, collection string, payloads []schema.PointPayload) error {
	if len(payloads) == 0 {
		return nil
	}
	body := map[string]any{
		"points": payloads,
		"merge":  true,
	}
	url := fmt.Sprintf("%s/collections/%s/points/payload?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("overwrite payload on %q (%d points): %w", collection, len(payloads), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// requireSchemaMatch is an internal helper used by the snapshot
// endpoints to fail fast on empty collection names + collection-not-found
// before issuing the HTTP call. Returns nil for valid input.
func (c *Client) requireSchemaMatch(collection string) error {
	if collection == "" {
		return fmt.Errorf("qdrant.Client: collection must not be empty")
	}
	return nil
}
