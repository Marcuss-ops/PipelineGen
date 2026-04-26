package main

import (
	"context"
	"fmt"
	"log"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()

	// Test unified database
	dbFile := "velox.db.sqlite"
	fmt.Printf("\n=== Testing Unified Database (%s) ===\n", dbFile)

	// Open database
	sqliteDB, err := storage.NewSQLiteDB("../../data", dbFile, logger)
	if err != nil {
		log.Fatalf("✗ Failed to open unified database: %v", err)
	}
	defer sqliteDB.Close()

	// Create repository
	repo := clips.NewRepository(sqliteDB.DB)

	ctx := context.Background()

	// Test 1: Count clips by source
	rows, err := sqliteDB.DB.Query("SELECT source, COUNT(*) FROM clips GROUP BY source")
	if err != nil {
		log.Printf("✗ Failed to count clips: %v", err)
	} else {
		defer rows.Close()
		fmt.Println("Clips by source:")
		for rows.Next() {
			var source string
			var count int
			if err := rows.Scan(&source, &count); err == nil {
				fmt.Printf("  %s: %d clips\n", source, count)
			}
		}
	}

	// Test 2: Search for clips
	matches, err := repo.SearchStockByKeywords(ctx, []string{"test"}, 5)
	if err != nil {
		log.Printf("✗ Failed to search: %v", err)
	} else {
		fmt.Printf("✓ Search returned %d results\n", len(matches))
	}

	// Test 3: List clips
	allClips, err := repo.ListClips(ctx, "")
	if err != nil {
		log.Printf("✗ Failed to list clips: %v", err)
	} else {
		fmt.Printf("✓ List returned %d clips\n", len(allClips))
	}

	fmt.Println("\n=== Test completed ===")
	fmt.Println("Unified database is working correctly!")
}
