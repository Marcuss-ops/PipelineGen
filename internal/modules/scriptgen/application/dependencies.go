// Package application hosts the use cases (hexagonal application layer)
// for the scriptgen module. It coordinates domain types via the ports
// declared in internal/modules/scriptgen/ports, and is the ONLY place
// where shell orchestration logic is allowed to live.
//
// The package MUST NOT import:
//   - github.com/gin-gonic/gin            (transport)
//   - database/sql, github.com/mattn/go-sqlite3 (SQLite)
//   - google.golang.org/api/drive/v3     (Drive SDK)
//   - qdrant client                       (semantic index)
//   - any package under internal/infrastructure/ or internal/application/jobs/
//
// Logging is delegated to go.uber.org/zap (interface-level); the optional
// Log dependency collapses to zap.NewNop() when nil.
package application

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/scriptgen/ports"
)

// Dependencies is the runtime-injected graph the scriptgen module
// needs to operate. Agent 1 (composition root owner) is responsible for
// wiring concrete adapters (SQLite, Ollama, Job, Qdrant, Drive) into
// the matching port interfaces.
type Dependencies struct {
	// Repo persists Script aggregates.
	Repo ports.ScriptRepository

	// LLM produces text from prompts.
	LLM ports.LLMGenerator

	// Jobs enqueues async generations.
	Jobs ports.JobSubmitter

	// Assets reads media asset metadata.
	Assets ports.AssetRepository

	// Search delegates semantic scene→clip matching.
	Search ports.AssetSearch

	// Docs produces output documents (Drive today).
	Docs ports.DocumentBuilder

	// Log is a structured logger; if nil, a no-op logger is used.
	Log *zap.Logger
}
