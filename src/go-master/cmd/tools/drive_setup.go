package main
import (
	"context"
	"fmt"
	"log"
	"velox/go-master/internal/upload/drive"
)
func main() {
	ctx := context.Background()
	client, err := drive.NewClient(ctx, "src/go-master/credentials.json", "src/go-master/token.json")
	if err != nil { log.Fatalf("Auth failed: %v", err) }
	
	parentID := "1ID_oFJF15Q5nmiZF0d2NaJeKhsOJpQNS"
	folderID, err := client.GetOrCreateFolder(ctx, "Various Clips", parentID)
	if err != nil { log.Fatalf("Folder creation failed: %v", err) }
	
	fmt.Printf("SUCCESS: Cartella 'Various Clips' creata/verificata. ID: %s\n", folderID)
}
