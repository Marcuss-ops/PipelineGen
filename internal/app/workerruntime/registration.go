// Package workerruntime — registration.go (P1-3, June 2026).
//
// Three responsibilities owned by this file:
//
//  1. LoadLogger — initialise the worker-scoped *zap.Logger. Run()
//     defers logging.Sync() so the lifetime is tied to ctx.
//
//  2. NewRegistrationClients — build the broker + asset-transfer
//     HTTP clients. Token precedence: $VELOX_WORKER_TOKEN.
//
//  3. RegisterWorkerSession — wire the canonical
//     appjobs.RegisterWorkerCommand shape (id/name/version/
//     hostname/capabilities/TTL) and run broker.RegisterWorker().
//     Returns the *appjobs.WorkerSession the runner goroutine
//     needs.
//
// A small helper initWorkspace() also lives here because the
// workspace is part of the worker "identity" surface (it gates
// job claim attempts).
package workerruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	logging "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remote/assettransferclient"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remote/jobbrokerclient"
)

// LoadLogger initialises the structured logger so Run() can mint
// the .Named("worker") logger used through the rest of the
// lifecycle. Idempotent on logging.Init: re-calls pick up the
// existing destination.
func LoadLogger(cfg *config.Config) (*zap.Logger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("LoadLogger: nil cfg (LoadConfig must run first)")
	}
	logging.Init(cfg.Logging.Level, cfg.Logging.Format)
	return logging.Get().Named("worker"), nil
}

// NewRegistrationClients builds the broker + asset-transfer client
// the worker uses for the heartbeat/registration/long-poll loop.
// Token precedence: $VELOX_WORKER_TOKEN.
func NewRegistrationClients(masterURL string) (appjobs.Broker, *assettransferclient.Client) {
	token := Env("VELOX_WORKER_TOKEN", "")
	broker := jobbrokerclient.New(masterURL, token)
	assetClient := assettransferclient.New(masterURL, token)
	return broker, assetClient
}

// RegisterWorkerSession runs broker.RegisterWorker with the
// canonical RegisterWorkerCommand shape and returns the resulting
// *appjobs.WorkerSession (worker_id + session_id + ttl surface).
//
// SessionTTL is hard-coded at 90s to match the production
// heartbeat interval ceiling. A future PR may expose this as a
// flag if a deployment needs a tighter SLA.
func RegisterWorkerSession(
	ctx context.Context,
	broker appjobs.Broker,
	identity Identity,
	caps appjobs.WorkerCapabilities,
) (*appjobs.WorkerSession, error) {
	return broker.RegisterWorker(ctx, appjobs.RegisterWorkerCommand{
		WorkerID:     identity.WorkerID,
		Name:         identity.WorkerName,
		Version:      identity.Version,
		Hostname:     identity.Hostname,
		Capabilities: caps,
		SessionTTL:   90 * time.Second,
	})
}

// initWorkspace creates the canonical <TempDir>/pipelinegen/jobs
// directory (idempotent mkdirall) and constructs the worker.Workspace
// that worker.NewRunner needs.
//
// workspaceRoot: <TempDir>/pipelinegen/jobs (the per-job artefact root)
// ws.Root:       <TempDir>/pipelinegen (one level up — handles
//
//	transfer-client staging layout)
//
// Returns the workspaceRoot path + the constructed *worker.Workspace,
// or an error covering either failure.
func initWorkspace() (string, *worker.Workspace, error) {
	workspaceRoot := filepath.Join(os.TempDir(), "pipelinegen", "jobs")
	if mkErr := os.MkdirAll(workspaceRoot, 0o755); mkErr != nil {
		return "", nil, fmt.Errorf("workspace mkdirall %q: %w", workspaceRoot, mkErr)
	}
	ws, err := worker.NewWorkspace(filepath.Join(os.TempDir(), "pipelinegen"))
	if err != nil {
		return "", nil, fmt.Errorf("worker.NewWorkspace: %w", err)
	}
	return workspaceRoot, ws, nil
}
