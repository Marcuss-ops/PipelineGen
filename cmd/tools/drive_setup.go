package main

import (
	"context"
	"fmt"
	"log"

	"velox/go-master/internal/upload/drive"
	appconfig "velox/go-master/pkg/config"
)

func main() {
	ctx := context.Background()
	cfg := drive.DefaultConfig()
	client, err := drive.NewClient(ctx, cfg)
	if err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	parentID := appconfig.Get().Drive.ClipsRootFolderID
	folderID, err := client.GetOrCreateFolder(ctx, "Various Clips", parentID)
	if err != nil {
		log.Fatalf("Folder creation failed: %v", err)
	}

	fmt.Printf("SUCCESS: Cartella 'Various Clips' creata/verificata. ID: %s\n", folderID)
}
