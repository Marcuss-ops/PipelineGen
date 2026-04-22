package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"velox/go-master/internal/pipeline/store"
)

func main() {
	videoID := flag.String("id", "", "YouTube Video ID to queue")
	dbPath := flag.String("db", "data/pipeline.db", "Path to pipeline SQLite DB")
	flag.Parse()

	if *videoID == "" {
		log.Fatal("Please provide a video ID using -id")
	}

	s, err := store.NewPipelineStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	err = s.AddToQueue(context.Background(), *videoID)
	if err != nil {
		log.Fatalf("Failed to add to queue: %v", err)
	}

	fmt.Printf("Video %s successfully added to persistent queue\n", *videoID)
}
