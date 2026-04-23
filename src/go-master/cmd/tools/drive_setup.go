package main
import (
	"context"
	"fmt"
	"log"
	"velox/go-master/internal/upload/drive"
)
func main() {
	ctx := context.Background()
	cfg := drive.DefaultConfig()
	cfg.CredentialsFile = "src/go-master/credentials.json"
	cfg.TokenFile = "src/go-master/token.json"
	client, err := drive.NewClient(ctx, cfg)
	if err != nil { log.Fatalf("Auth failed: %v", err) }
	
	parentID := "1ID_oFJF15Q5nmiZF0d2NaJeKhsOJpQNS"
	folderID, err := client.GetOrCreateFolder(ctx, "Various Clips", parentID)
	if err != nil { log.Fatalf("Folder creation failed: %v", err) }
	
	fmt.Printf("SUCCESS: Cartella 'Various Clips' creata/verificata. ID: %s\n", folderID)
}
