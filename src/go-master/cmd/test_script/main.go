package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
)

func main() {
	// Initialize SQLite DB
	dataDir := "/tmp/velox_test_data"
	os.MkdirAll(dataDir, 0755)
	dbName := "velox_test.db.sqlite"
	dbPath := filepath.Join(dataDir, dbName)
	os.Remove(dbPath)

	db, err := storage.NewSQLiteDB(dataDir, dbName, zap.NewNop())
	if err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	// Initialize repositories
	stockRepo := clips.NewRepository(db.DB)
	artlistRepo := clips.NewRepository(db.DB)

	// Initialize Ollama generator
	gen := ollama.NewGenerator(client.NewClient("http://localhost:11434", "gemma3:4b"))

	// Create script request
	req := script.ScriptDocsRequest{
		Topic:       "Mike Tyson boxing career highlights",
		Duration:    120,
		Language:    "en",
		Template:    "documentary",
		PreviewOnly: true,
	}

	// Set up directories
	clipTextDir := "/tmp/velox_clips"
	os.MkdirAll(clipTextDir, 0755)
	pythonScriptsDir := "/home/pierone/Pyt/VeloxEditing/refactored/scripts"
	nodeScraperDir := "/home/pierone/Pyt/VeloxEditing/refactored/scripts/harvest"

	// Build script document
	ctx := context.Background()
	doc, err := script.BuildScriptDocument(ctx, gen, req, dataDir, clipTextDir, pythonScriptsDir, nodeScraperDir, stockRepo, artlistRepo)
	if err != nil {
		log.Fatalf("Failed to build script: %v", err)
	}

	// Output results
	fmt.Println("=== SCRIPT GENERATED ===")
	fmt.Printf("Title: %s\n\n", doc.Title)

	// Save to file
	outputPath := filepath.Join(dataDir, "generated_script.txt")
	err = os.WriteFile(outputPath, []byte(doc.Content), 0644)
	if err != nil {
		log.Printf("Failed to save script: %v", err)
	} else {
		fmt.Printf("Script saved to: %s\n", outputPath)
	}

	// Show timeline if available
	if doc.Timeline != nil && len(doc.Timeline.Segments) > 0 {
		fmt.Println("\n=== TIMELINE SEGMENTS ===")
		for i, seg := range doc.Timeline.Segments {
			fmt.Printf("\n[%d] %s\n", i+1, seg.Timestamp)
			fmt.Printf("    Opening: %s\n", seg.OpeningSentence)
			fmt.Printf("    Closing: %s\n", seg.ClosingSentence)
			fmt.Printf("    Keywords: %v\n", seg.Keywords)
			fmt.Printf("    Stock matches: %d\n", len(seg.StockMatches))
			fmt.Printf("    Artlist matches: %d\n", len(seg.ArtlistMatches))
		}
	}

	fmt.Println("\n=== SCRIPT CONTENT (first 1000 chars) ===")
	content := doc.Content
	if len(content) > 1000 {
		content = content[:1000] + "..."
	}
	fmt.Println(content)
}
