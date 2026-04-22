package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
)

func main() {
	fmt.Println("Starting indexer test...")

	ctx := context.Background()

	config := drive.Config{
		CredentialsFile: "../../credentials.json",
		TokenFile:       "../../token.json",
		Scopes: []string{
			"https://www.googleapis.com/auth/drive",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive.readonly",
		},
	}

	fmt.Println("Creating Drive client...")
	client, err := drive.NewClient(ctx, config)
	if err != nil {
		fmt.Println("Error creating client:", err)
		os.Exit(1)
	}
	fmt.Println("Drive client created OK")

	fmt.Println("Creating indexer...")
	indexer := clip.NewIndexer(client, "root")

	fmt.Println("Starting scan (depth=2, max 50 per folder)...")
	startTime := time.Now()

	ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err = indexer.ScanAndIndex(ctx2)
	if err != nil {
		fmt.Println("Scan error:", err)
		os.Exit(1)
	}

	fmt.Printf("Scan completed in %v\n", time.Since(startTime))

	stats := indexer.GetStats()
	fmt.Printf("Total clips: %d\n", stats.TotalClips)
	fmt.Printf("Total folders: %d\n", stats.TotalFolders)
	fmt.Printf("Clips by group: %v\n", stats.ClipsByGroup)

	fmt.Println("\nTest completed successfully!")
}
