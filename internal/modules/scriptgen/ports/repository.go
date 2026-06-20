// Package ports defines the hexagonal ports (interfaces) that the
// scriptgen module consumes. Concrete adapters (SQLite, Ollama HTTP,
// Qdrant, Google Drive SDK, ... ) live in internal/infrastructure/ and
// are wired in by Agent 1 through the Dependencies struct.
//
// This package MUST NOT import:
//   - database/sql, github.com/mattn/go-sqlite3 (SQLite)
//   - github.com/gin-gonic/gin (transport)
//   - google.golang.org/api/drive/v3 (Drive)
//   - qdrant client (Qdrant)
//   - any package under internal/infrastructure/
//
// The only module-internal dependency allowed is the local domain/types.
package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/scriptgen/domain"
)

// ScriptRepository abstracts persistence of Script aggregates. It
// deliberately uses only domain types so the storage engine choice stays
// out of the scriptgen module.
type ScriptRepository interface {
	GetByID(ctx context.Context, id int64) (*domain.Script, error)
	Insert(ctx context.Context, s *domain.Script) (int64, error)
	Update(ctx context.Context, s *domain.Script) error
	Delete(ctx context.Context, id int64) error
	ListByStatus(ctx context.Context, status string, limit, offset int) ([]domain.Script, error)
}
