// Package app — BuildOutboxBundle DriveDeleteHandler wiring test
// (Blocco 3.1 commit 2/3 regression pin, July 2026).
//
// Before the fix, outboxDeps.DriveDelete was NEVER populated by
// BuildOutboxBundle: RegisterOptionalHandlers saw all four narrow
// ports nil and skipped DriveDeleteHandler, so every
// asset.drive.delete_requested.v1 event dead-lettered with "no
// handler registered for event type asset.drive.delete_requested"
// (186 dead-letters observed in production). This test pins the
// regression: with ClipsRepo + dispatcher + the driveDeleter arg all
// wired, the handler MUST be registered in the returned
// EventsRegistry.
package app

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// dummyDriveDeleter satisfies jobsoutbox.DriveDeleter for the wiring
// test (Trash + Delete, no-op bodies).
type dummyDriveDeleter struct{}

func (dummyDriveDeleter) Trash(ctx context.Context, fileID string) error  { return nil }
func (dummyDriveDeleter) Delete(ctx context.Context, fileID string) error { return nil }

// TestBuildOutboxBundle_WiresDriveDeleteHandler pins the canonical
// production wiring: when all four DriveDeleteDeps ports are available
// (ClipsRepo, dispatcher, driveDeleter), the outbox registry MUST
// contain a handler for asset.drive.delete_requested.v1.
func TestBuildOutboxBundle_WiresDriveDeleteHandler(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.Qdrant.Enabled = true
	cfg.ClipIndexer.Enabled = true
	log := zaptest.NewLogger(t)

	dbs, err := wiring.InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err)

	qd, err := buildQdrantDeps(context.Background(), cfg, dbs, repos, log)
	require.NoError(t, err)

	jobsBundle, err := wiring.BuildJobsBundle(dbs.Main, log, nil, nil, nil, nil)
	require.NoError(t, err)

	outbox, _, err := BuildOutboxBundle(context.Background(), cfg, dbs, log, repos, qd, jobsBundle, nil, dummyStagingStore{}, dummyArtifactRepo{}, dummyPublisher{}, dummyDriveDeleter{})
	require.NoError(t, err, "BuildOutboxBundle with all DriveDelete ports wired")

	h, ok := outbox.EventsRegistry.Get(outboxevents.EventAssetDriveDeleteRequested)
	require.True(t, ok, "asset.drive.delete_requested.v1 handler must be registered when DriveDeleteDeps is wired (Blocco 3.1 commit 2/3 wiring regression)")
	require.NotNil(t, h, "registered handler must be non-nil")
	require.Equal(t, outboxevents.EventAssetDriveDeleteRequested, h.EventType())
}
