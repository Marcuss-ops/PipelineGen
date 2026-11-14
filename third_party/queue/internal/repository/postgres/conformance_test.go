package postgres

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/repositorytest"
)

// TestRepositoryConformance runs the SHARED contract suite against the real
// PostgreSQL backend. Without TEST_DATABASE_URL the suite skips through
// testDatabaseURL, and RENDERINGGEN_REQUIRE_POSTGRES_TESTS=1 turns that skip
// into a failure so CI cannot report a green cross-backend run that never
// touched PostgreSQL.
func TestRepositoryConformance(t *testing.T) {
	repositorytest.Run(t, func(t *testing.T) repository.JobRepository {
		return newRepo(t, 30*time.Second, 3)
	})
}
