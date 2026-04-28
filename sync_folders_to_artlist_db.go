package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
	"database/sql"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: sync_folders_to_artlist_db <credentials.json>")
		os.Exit(1)
	}

	credsPath := os.Args[1]
	
	// Folder IDs to sync
	folders := []string{
		"1NUWT1bont3RvIYHLaLdFJs9fTammbX4e",
		"1E0oJDJf1MZkNiORX3Yb_9v2eM5QfHh1A",
		"12ncviAMoZCl1qW2ZIay5BI0N-5R7aROD",
		"16vLXqbrYYM0iyBxdFtb7WX6Watyd5kF2",
		"1tzfqvPsk3pu1EeDKvsXPBEvh_Yqi1AIQ",
	}

	// Initialize logger
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// Read credentials
	credsData, err := ioutil.ReadFile(credsPath)
	if err != nil {
		log.Fatalf("Failed to read credentials: %v", err)
	}

	// Create Drive client
	config, err := google.ConfigFromJSON(credsData, drive.DriveScope)
	if err != nil {
		log.Fatalf("Failed to parse credentials: %v", err)
	}

	// Check for token
	tokenPath := "token.json"
	tok, err := loadToken(tokenPath)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokenPath, tok)
	}

	client := config.Client(context.Background(), tok)
	driveService, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Failed to create Drive service: %v", err)
	}

	// Open artlist database
	dbPath := filepath.Join("src", "node-scraper", "artlist_videos.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Sync each folder
	ctx := context.Background()
	for _, folderID := range folders {
		if err := syncFolderToArtlistDB(ctx, driveService, db, folderID); err != nil {
			logger.Error("Failed to sync folder", zap.String("folder_id", folderID), zap.Error(err))
		}
	}

	fmt.Println("Done!")
}

func syncFolderToArtlistDB(ctx context.Context, svc *drive.Service, db *sql.DB, folderID string) error {
	// Get folder metadata
	folder, err := svc.Files.Get(folderID).Fields("id,name,mimeType,parents,driveId,modifiedTime,size").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	driveLink := fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID)
	
	// Check if folder already exists
	var existingID string
	err = db.QueryRow("SELECT drive_id FROM artlist_folders WHERE drive_id = ?", folderID).Scan(&existingID)
	if err == nil {
		fmt.Printf("Folder already exists: %s (%s)\n", folder.Name, folderID)
		return nil
	}

	// Insert folder
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO artlist_folders 
		(drive_id, name, full_path, parent_drive_id, drive_link, mime_type, modified_time, size_bytes, trashed, last_seen_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		folderID, folder.Name, folder.Name, "", driveLink, folder.MimeType, folder.ModifiedTime, 0, 0)
	
	if err != nil {
		return fmt.Errorf("failed to insert folder: %w", err)
	}

	fmt.Printf("Added folder: %s (%s)\n", folder.Name, folderID)
	return nil
}

func loadToken(path string) (*oauth2.Token, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tok := &oauth2.Token{}
	json.Unmarshal(data, tok)
	return tok, nil
}

func saveToken(path string, token *oauth2.Token) {
	data, _ := json.Marshal(token)
	ioutil.WriteFile(path, data, 0600)
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to: %s\n", authURL)
	fmt.Print("Enter code: ")
	var code string
	fmt.Scan(&code)
	tok, _ := config.Exchange(context.Background(), code)
	return tok
}
