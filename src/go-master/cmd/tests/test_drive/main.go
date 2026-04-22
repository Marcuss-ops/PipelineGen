package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func main() {
	// Load token
	tokenPath := "token.json"
	if p := os.Getenv("VELOX_TOKEN_PATH"); p != "" {
		tokenPath = p
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Println("Error reading token:", err)
		os.Exit(1)
	}

	var tokenData map[string]interface{}
	if err := json.Unmarshal(data, &tokenData); err != nil {
		fmt.Println("Error parsing token:", err)
		os.Exit(1)
	}

	accessToken := tokenData["token"].(string)
	
	// Create static token source (no refresh)
	token := &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	}

	staticSource := oauth2.StaticTokenSource(token)

	// Create Drive client
	ctx := context.Background()
	client := oauth2.NewClient(ctx, staticSource)
	
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Println("Error creating Drive service:", err)
		os.Exit(1)
	}

	// Test call
	folders, err := srv.Files.List().
		Q("mimeType='application/vnd.google-apps.folder' and 'root' in parents and trashed=false").
		Fields("files(id, name)").
		PageSize(10).
		Do()

	if err != nil {
		fmt.Println("Error listing folders:", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d folders:\n", len(folders.Files))
	for _, f := range folders.Files {
		fmt.Printf("  - %s (%s)\n", f.Name, f.Id)
	}
}
