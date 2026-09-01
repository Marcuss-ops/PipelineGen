package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestBackupSetCreatesDurablePlaneManifestAndExcludesCache(t *testing.T) {
	root := t.TempDir()
	set, err := OpenSet(StorageConfig{
		DataDir:     root,
		JobsDBPath:  filepath.Join(root, "jobs", "jobs.db.sqlite"),
		CacheDBPath: filepath.Join(root, "cache", "cache.db.sqlite"),
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	if _, err := set.Primary.Exec(`CREATE TABLE backup_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "backups", "snapshot-1")
	manifest, err := set.BackupSet(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CachePolicy != "excluded; rebuild on miss" {
		t.Fatalf("cache policy = %q", manifest.CachePolicy)
	}
	if len(manifest.Planes) != 3 {
		t.Fatalf("durable planes = %d, want 3", len(manifest.Planes))
	}
	for _, plane := range manifest.Planes {
		if plane.Backup.SHA256 == "" || plane.Backup.SizeBytes == 0 {
			t.Fatalf("incomplete backup metadata for %s: %+v", plane.Name, plane.Backup)
		}
		if _, err := os.Stat(plane.Backup.Path); err != nil {
			t.Fatalf("backup file for %s: %v", plane.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "cache.db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("cache backup must be absent, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}
