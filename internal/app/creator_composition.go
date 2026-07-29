// Package app — creator_composition.go (Creator Blocco 3.1, July 2026).
//
// InitCreatorComposition builds a minimal wiring.ComposeRoot-like graph for the
// Creator worker profile. Unlike InitWorkerComposition, this path:
//
//   - Does NOT open wiring.Databases (no SQLite, no migrations).
//   - Does NOT construct Drive, Qdrant, Repos, or the full wiring.ComposeRoot.
//   - Builds ONLY the services the Creator needs: Ollama client → script
//     engine → script.generate handler + voiceover.generate_item handler.
//   - Uses a temporary workspace under /tmp/pipelinegen/creator/.
//   - Wires a remote asset client (veloxclient) for talking to the Sender.
//
// The returned CreatorRoot carries a pre-built worker.Registry so run.go
// can feed it directly to worker.NewRunner without building a registry
// from wiring.ComposeRoot.
//
// P0 Commit 8 (July 2026): InitCreatorComposition is now a THIN SHIM that
// delegates to BuildCreatorRuntime (the canonical Creator-side wiring
// entry point in creator_runtime.go). Body moved out so the canonical
// Creator-side surface lives in ONE file (creator_runtime.go) with its
// no-DB / no-Qdrant / no-Scheduler / no-CatalogSync contract enforced
// via compile-time orphan pin + import-allowlist AST scan. Future
// workerruntime/run.go Creator profile (Blocco 4) will retire this shim
// entirely once no other call site references CreatorRoot.
package app

// P0 Commit 8 (July 2026, C8): InitCreatorComposition is now a THIN
// SHIM. All Creator-side wiring lives in BuildCreatorRuntime
// (creator_runtime.go) so the canonical no-DB/no-Qdrant/no-Scheduler/
// no-CatalogSync contract can be enforced via compile-time orphan
// pin + import-allowlist AST scan in ONE file. Only the deps the
// shim itself references are imported here.
import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// InitCreatorComposition builds the Creator service graph from config.
// Returns the assembled CreatorRoot (= CreatorRuntime type alias), a
// wiring.CleanupFunc that removes the temporary workspace, and an error if any
// required service fails to initialise.
//
// The returned wiring.CleanupFunc must be called exactly once when the worker
// shuts down (typically via defer in run.go).
//
// P0 Commit 8 (July 2026): InitCreatorComposition is now a THIN SHIM
// that delegates to BuildCreatorRuntime (the canonical Creator-side
// factory in creator_runtime.go). The implementation has been moved
// out so the canonical Creator-side surface lives in ONE file with
// its no-DB/no-Qdrant/no-Scheduler/no-CatalogSync contract enforced
// via compile-time orphan pin + import-allowlist AST scan (see
// creator_runtime_test.go). Future Blocco 4 commits will retire this
// shim entirely once workerruntime/run.go Creator profile is the
// only call site that references CreatorRoot (alias).
func InitCreatorComposition(cfg *config.Config, log *zap.Logger) (*CreatorRoot, wiring.CleanupFunc, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("creator composition: config is nil")
	}
	if log == nil {
		return nil, nil, fmt.Errorf("creator composition: logger is nil")
	}
	rt, cleanup, err := BuildCreatorRuntime(cfg, log)
	if err != nil {
		return nil, nil, fmt.Errorf("creator composition: %w", err)
	}
	// BuildCreatorRuntime returns app.wiring.CleanupFunc (the canonical
	// package-level func type). The shim's declared return type is
	// the same wiring.CleanupFunc — identity conversion, no allocation.
	return rt, cleanup, nil
}
