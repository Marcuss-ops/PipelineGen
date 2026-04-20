package stockdb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestStockDB_ConcurrentWrites(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stockdb_test")
	defer os.RemoveAll(tempDir)
	dbPath := filepath.Join(tempDir, "test.sqlite")

	db, err := Open(dbPath)
	assert.NoError(t, err)
	defer db.Close()

	var eg errgroup.Group
	numWrites := 50

	// Test scrittura concorrente di clip
	for i := 0; i < numWrites; i++ {
		id := fmt.Sprintf("clip-%d", i)
		eg.Go(func() error {
			return db.UpsertClip(StockClipEntry{
				ClipID:   id,
				Filename: id + ".mp4",
				Source:   "test",
				Tags:     []string{"tag1", "tag2"},
			})
		})
	}

	// Test scrittura concorrente di folder
	for i := 0; i < numWrites; i++ {
		slug := fmt.Sprintf("topic-%d", i)
		eg.Go(func() error {
			return db.UpsertFolder(StockFolderEntry{
				TopicSlug: slug,
				DriveID:   "drive-" + slug,
				Section:   "stock",
			})
		})
	}

	assert.NoError(t, eg.Wait())

	stats := db.GetStats()
	assert.Equal(t, numWrites, stats["clips"])
	assert.Equal(t, numWrites, stats["folders"])
}

func TestStockDB_MigrationFromJSON(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "migration_test")
	defer os.RemoveAll(tempDir)
	jsonPath := filepath.Join(tempDir, "stock.db.json")

	// 1. Crea un file JSON legacy
	legacyData := `{
		"folders": [
			{"topic_slug": "legacy-1", "drive_id": "d1", "section": "stock"}
		],
		"clips": [
			{"clip_id": "c1", "filename": "f1.mp4", "tags": ["old"]}
		]
	}`
	_ = os.WriteFile(jsonPath, []byte(legacyData), 0644)

	// 2. Apri il DB (dovrebbe migrare)
	db, err := Open(jsonPath)
	assert.NoError(t, err)
	defer db.Close()

	// 3. Verifica dati migrati
	stats := db.GetStats()
	assert.Equal(t, 1, stats["folders"])
	assert.Equal(t, 1, stats["clips"])

	// 4. Verifica backup
	_, err = os.Stat(jsonPath + ".bak")
	assert.NoError(t, err)
}
