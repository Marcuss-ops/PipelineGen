package sqlite

import (
	"context"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestDatabaseSetMigrateEnforcesControlPlaneIdentity(t *testing.T) {
	root := t.TempDir()
	set, err := OpenSet(StorageConfig{
		PrimaryDBPath:       root + "/primary.sqlite",
		ObservabilityDBPath: root + "/observability.sqlite",
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if err := set.Migrate(zaptest.NewLogger(t)); err != nil {
		t.Fatalf("canonical DatabaseSet migration rejected: %v", err)
	}
	if err := set.ValidateControlPlaneIdentity(context.Background()); err != nil {
		t.Fatalf("canonical identity rejected after migration 198: %v", err)
	}

	if _, err := set.Primary.Exec(`UPDATE control_plane_meta SET instance_role = 'READ_ONLY'`); err != nil {
		t.Fatal(err)
	}
	if err := set.Migrate(zaptest.NewLogger(t)); err == nil {
		t.Fatal("DatabaseSet migration accepted a READ_ONLY primary as the operational SSOT")
	}
}
