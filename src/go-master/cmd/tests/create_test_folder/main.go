package main

import (
	"context"
	"fmt"
	"log"

	"velox/go-master/internal/upload/drive"
)

func main() {
	ctx := context.Background()

	// Google Drive folder ID from the link
	parentFolderID := "1ID_oFJF15Q5nmiZF0d2NaJeKhsOJpQNS"
	folderName := "test"

	// Initialize Drive client with credentials
	config := drive.DefaultConfig()
	config.CredentialsFile = "credentials.json"
	config.TokenFile = "token.json"

	client, err := drive.NewClient(ctx, config)
	if err != nil {
		log.Fatalf("❌ Failed to initialize Drive client: %v", err)
	}

	// Create folder using the fixed GetOrCreateFolder method
	folderID, err := client.GetOrCreateFolder(ctx, folderName, parentFolderID)
	if err != nil {
		log.Fatalf("❌ Failed to create folder: %v", err)
	}

	fmt.Printf("✅ Folder created successfully!\n")
	fmt.Printf("📁 Folder name: %s\n", folderName)
	fmt.Printf("🆔 Folder ID: %s\n", folderID)
	fmt.Printf("📍 Parent folder ID: %s\n", parentFolderID)
	fmt.Printf("🔗 Drive link: https://drive.google.com/drive/folders/%s\n", folderID)
}
