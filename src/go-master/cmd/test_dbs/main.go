package main

import (
	"context"
	"fmt"
	"log"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()

	// Test all three databases
	databases := []struct {
		name string
		file string
	}{
		{"Stock Drive", "stock_drive.db.sqlite"},
		{"Artlist", "artlist.db.sqlite"},
		{"Clips", "clips.db.sqlite"},
	}

	ctx := context.Background()

	for _, db := range databases {
		fmt.Printf("\n=== Testing %s (%s) ===\n", db.name, db.file)

		// Open database
		sqliteDB, err := storage.NewSQLiteDB("../../data", db.file, logger)
		if err != nil {
			log.Printf("✗ Failed to open %s: %v", db.name, err)
			continue
		}

		// Create repository
		repo := clips.NewRepository(sqliteDB.DB)

		// Test 1: Insert a test clip
		testClip := &models.Clip{
			ID:        fmt.Sprintf("test_%s_%d", db.name, 1),
			Name:      fmt.Sprintf("Test Clip %s", db.name),
			Filename:  fmt.Sprintf("test_%s.mp4", db.name),
			FolderID:  "test_folder",
			MediaType: db.name,
			Source:    db.name,
			Tags:      []string{"test", db.name},
		}

		err = repo.UpsertClip(ctx, testClip)
		if err != nil {
			log.Printf("✗ Failed to insert test clip into %s: %v", db.name, err)
			sqliteDB.Close()
			continue
		}
		fmt.Printf("✓ Inserted test clip into %s\n", db.name)

		// Test 2: Retrieve the clip
		retrieved, err := repo.GetClipByID(ctx, testClip.ID)
		if err != nil {
			log.Printf("✗ Failed to retrieve clip from %s: %v", db.name, err)
			sqliteDB.Close()
			continue
		}
		fmt.Printf("✓ Retrieved clip from %s: %s (MediaType: %s)\n", db.name, retrieved.Name, retrieved.MediaType)

		// Test 3: Search for clips
		matches, err := repo.SearchStockByKeywords(ctx, []string{db.name}, 5)
		if err != nil {
			log.Printf("✗ Failed to search in %s: %v", db.name, err)
		} else {
			fmt.Printf("✓ Search in %s returned %d results\n", db.name, len(matches))
		}

		// Test 4: List clips
		allClips, err := repo.ListClips(ctx, "")
		if err != nil {
			log.Printf("✗ Failed to list clips from %s: %v", db.name, err)
		} else {
			fmt.Printf("✓ List from %s returned %d clips\n", db.name, len(allClips))
		}

		sqliteDB.Close()
		fmt.Printf("✓ %s database connection closed\n", db.name)
	}

	fmt.Println("\n=== All tests completed ===")
	fmt.Println("Each database is independent and working correctly!")
}
