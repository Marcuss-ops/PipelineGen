// Script per scansionare TUTTE le cartelle Stock su Drive
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	appconfig "velox/go-master/pkg/config"
)

type FolderTree struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	URL        string       `json:"url"`
	ParentID   string       `json:"parent_id"`
	Subfolders []FolderTree `json:"subfolders"`
	Clips      []ClipInfo   `json:"clips"`
}

type ClipInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Duration int64  `json:"duration_ms"`
	Width    int64  `json:"width"`
	Height   int64  `json:"height"`
	Size     int64  `json:"size"`
}

func main() {
	ctx := context.Background()

	// Load credentials
	cfg := appconfig.Get()

	creds, err := os.ReadFile(cfg.GetCredentialsPath())
	if err != nil {
		fmt.Printf("❌ Cannot read credentials file: %v\n", err)
		os.Exit(1)
	}

	oauthConfig, err := google.ConfigFromJSON(creds, drive.DriveReadonlyScope)
	if err != nil {
		fmt.Printf("❌ Invalid credentials: %v\n", err)
		os.Exit(1)
	}

	// Load token
	token, err := loadToken(cfg.GetTokenPath())
	if err != nil {
		fmt.Printf("❌ Cannot load token: %v\n", err)
		os.Exit(1)
	}

	// Create client
	client := oauthConfig.Client(context.Background(), token)

	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Printf("❌ Cannot create Drive service: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("📂 Google Drive Stock Scanner")
	fmt.Println("=" + strings.Repeat("=", 59))

	stockFolderIDs := cfg.DriveScan.StockFolderIDs
	clipsFolderIDs := cfg.DriveScan.ClipsFolderIDs

	fmt.Printf("\n📋 Scanning %d Stock folders + %d Clips folders...\n",
		len(stockFolderIDs), len(clipsFolderIDs))

	var allStockTrees []FolderTree
	var allClipsTrees []FolderTree
	totalFolders := 0
	totalClips := 0

	// Scan Stock
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Println("📦 STOCK SECTION")
	fmt.Println(strings.Repeat("─", 60))
	for _, folderID := range stockFolderIDs {
		f, err := srv.Files.Get(folderID).Fields("id", "name").Do()
		if err != nil {
			fmt.Printf("⚠️  Cannot access folder %s: %v\n", folderID, err)
			continue
		}
		fmt.Printf("\n🔍 Scanning Stock: %s (ID: %s)\n", f.Name, f.Id)
		tree := scanFolder(srv, f.Id, f.Name, "Stock")
		folders, clips := countAll(tree)
		totalFolders += folders
		totalClips += clips
		allStockTrees = append(allStockTrees, tree)
		fmt.Printf("  ✅ %d folders, %d clips\n", folders, clips)
	}

	// Scan Clips
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Println("🎬 CLIPS SECTION")
	fmt.Println(strings.Repeat("─", 60))
	for _, folderID := range clipsFolderIDs {
		f, err := srv.Files.Get(folderID).Fields("id", "name").Do()
		if err != nil {
			fmt.Printf("⚠️  Cannot access folder %s: %v\n", folderID, err)
			continue
		}
		fmt.Printf("\n🔍 Scanning Clips: %s (ID: %s)\n", f.Name, f.Id)
		tree := scanFolder(srv, f.Id, f.Name, "Clips")
		folders, clips := countAll(tree)
		totalFolders += folders
		totalClips += clips
		allClipsTrees = append(allClipsTrees, tree)
		fmt.Printf("  ✅ %d folders, %d clips\n", folders, clips)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Printf("📊 TOTALS\n")
	fmt.Printf("   Folders: %d\n", totalFolders)
	fmt.Printf("   Clips:   %d\n", totalClips)
	fmt.Println(strings.Repeat("=", 60))

	// Print full tree
	for _, tree := range allStockTrees {
		printTree(tree, 0)
	}
	for _, tree := range allClipsTrees {
		printTree(tree, 0)
	}

	// Save to JSON
	output := map[string]interface{}{
		"stock_root":    "Multiple folders",
		"total_folders": totalFolders,
		"total_clips":   totalClips,
		"stock_folders": allStockTrees,
		"clips_folders": allClipsTrees,
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Printf("❌ JSON marshal error: %v\n", err)
		os.Exit(1)
	}

	outputFile := appconfig.ResolveDataPath("stock_drive_structure.json")
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		fmt.Printf("❌ Cannot create output dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		fmt.Printf("❌ Cannot write file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n💾 Saved to: %s\n", outputFile)
	fmt.Println("✅ Done!")
}

func findStockRoot(srv *drive.Service) (string, error) {
	r, err := srv.Files.List().
		Q("mimeType='application/vnd.google-apps.folder' and name='Stock' and trashed=false").
		Fields("files(id, name)").
		Do()
	if err != nil {
		return "", err
	}

	if len(r.Files) == 0 {
		return "", fmt.Errorf("no Stock folder found")
	}

	return r.Files[0].Id, nil
}

func scanFolder(srv *drive.Service, folderID, folderName, parentPath string) FolderTree {
	tree := FolderTree{
		ID:   folderID,
		Name: folderName,
		Path: folderName,
	}

	if parentPath != "" {
		tree.Path = parentPath + "/" + folderName
	}

	// Get folder link
	tree.URL = fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID)
	tree.ParentID = parentPath

	// List subfolders
	subfolders, err := srv.Files.List().
		Q(fmt.Sprintf("'%s' in parents and mimeType='application/vnd.google-apps.folder' and trashed=false", folderID)).
		Fields("files(id, name)").
		OrderBy("name").
		Do()

	if err != nil {
		fmt.Printf("  ⚠️  Error listing folders of %s: %v\n", folderName, err)
		return tree
	}

	for _, f := range subfolders.Files {
		subTree := scanFolder(srv, f.Id, f.Name, tree.Path)
		tree.Subfolders = append(tree.Subfolders, subTree)
	}

	// List clips (video files)
	clips, err := srv.Files.List().
		Q(fmt.Sprintf("'%s' in parents and mimeType contains 'video/' and trashed=false", folderID)).
		Fields("files(id, name, webViewLink, videoMediaMetadata, size)").
		OrderBy("name").
		Do()

	if err != nil {
		fmt.Printf("  ⚠️  Error listing clips of %s: %v\n", folderName, err)
		return tree
	}

	for _, f := range clips.Files {
		clip := ClipInfo{
			ID:   f.Id,
			Name: f.Name,
			URL:  f.WebViewLink,
			Size: f.Size,
		}

		if f.VideoMediaMetadata != nil {
			clip.Duration = int64(f.VideoMediaMetadata.DurationMillis)
			clip.Width = int64(f.VideoMediaMetadata.Width)
			clip.Height = int64(f.VideoMediaMetadata.Height)
		}

		tree.Clips = append(tree.Clips, clip)
	}

	return tree
}

func countAll(tree FolderTree) (folders, clips int) {
	folders = 1
	clips = len(tree.Clips)

	for _, sub := range tree.Subfolders {
		f, c := countAll(sub)
		folders += f
		clips += c
	}

	return
}

func printTree(tree FolderTree, depth int) {
	indent := strings.Repeat("  ", depth)

	if depth == 0 {
		fmt.Printf("\n📁 %s (ID: %s)\n", tree.Name, tree.ID)
	} else {
		fmt.Printf("%s📁 %s (ID: %s)\n", indent, tree.Name, tree.ID)
	}

	if len(tree.Clips) > 0 {
		for _, clip := range tree.Clips {
			fmt.Printf("%s  🎬 %s (%.1fs, %dx%d)\n",
				indent, clip.Name, float64(clip.Duration)/1000.0, clip.Width, clip.Height)
		}
	}

	for _, sub := range tree.Subfolders {
		printTree(sub, depth+1)
	}
}

func loadToken(file string) (*oauth2.Token, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	return &token, nil
}
