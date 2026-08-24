// Package legacyaudit — SeedUUID is a deterministic UUID v5 helper
// used only by tests, isolated here so production code does not
// accidentally depend on the aid:* namespace below.

package audit

import "github.com/google/uuid"

// testNamespace is a project-local UUID v5 namespace used solely by
// the test helpers in this package. Production canonicalisation uses
// internal/platform/qdrant.PipelineGenQdrantNamespace; this
// helper is for test fixtures only.
var testNamespace = uuid.MustParse("9c1f4b2a-1111-4ddd-9c63-1a2b3c4d5e6f")

// SeedUUID returns a deterministic UUID v5 hash derived from seed and
// the test-local namespace. Different seeds produce different UUIDs;
// the same seed always produces the same UUID across runs.
func SeedUUID(seed string) string {
	if seed == "" {
		return ""
	}
	return uuid.NewSHA1(testNamespace, []byte(seed)).String()
}
