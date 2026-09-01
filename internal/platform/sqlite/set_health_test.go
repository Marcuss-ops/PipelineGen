package sqlite

import (
	"testing"

	"go.uber.org/zap"
)

func TestHealthByPlaneKeepsPlanesIndependent(t *testing.T) {
	set, err := OpenSet(StorageConfig{DataDir: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	health := set.HealthByPlane(t.Context())
	if !health["media"].Available || !health["observability"].Available || !health["cache"].Available {
		t.Fatalf("expected configured planes healthy: %+v", health)
	}
	if health["jobs"].Available {
		t.Fatal("jobs must be absent/degraded when split is disabled")
	}
}

func TestHealthByPlaneReportsMissingOptionalPlanes(t *testing.T) {
	set := &DatabaseSet{}
	health := set.HealthByPlane(t.Context())
	if health["media"].Available || health["cache"].Available {
		t.Fatalf("missing planes must not report available: %+v", health)
	}
}
