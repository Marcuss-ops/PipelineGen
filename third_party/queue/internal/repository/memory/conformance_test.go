package memory

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/repositorytest"
)

// TestRepositoryConformance runs the SHARED contract suite against the in-memory
// backend. The same suite runs against PostgreSQL in the postgres package; a
// behaviour asserted in only one backend is a claim about that backend, not
// about the storage contract.
func TestRepositoryConformance(t *testing.T) {
	repositorytest.Run(t, func(t *testing.T) repository.JobRepository {
		return New(30*time.Second, 3)
	})
}
