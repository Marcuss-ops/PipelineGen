package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type clipDriveRecord struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Filename     string   `json:"filename"`
	FolderID     string   `json:"folder_id"`
	FolderPath   string   `json:"folder_path"`
	Group        string   `json:"group"`
	MediaType    string   `json:"media_type"`
	DriveLink    string   `json:"drive_link"`
	DownloadLink string   `json:"download_link"`
	Tags         []string `json:"tags"`
}

type clipDriveIndex struct {
	Clips      []clipDriveRecord `json:"clips"`
	LastUpdate string            `json:"last_update"`
}

func main() {
	downloadDir := flag.String("dir", "./data/downloads", "Directory containing downloaded videos")
	outputFile := flag.String("out", "./data/clip_index.json", "Output JSON file path")
	flag.Parse()

	log.Printf("🔍 Scansione directory: %s", *downloadDir)

	index := clipDriveIndex{
		Clips:      []clipDriveRecord{},
		LastUpdate: time.Now().Format(time.RFC3339),
	}

	err := filepath.WalkDir(*downloadDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Supportati solo video
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".mp4" && ext != ".mkv" && ext != ".mov" && ext != ".avi" {
			return nil
		}

		relPath, _ := filepath.Rel(*downloadDir, path)
		folderPath := filepath.Dir(relPath)
		if folderPath == "." {
			folderPath = "General"
		}

		filename := d.Name()
		name := strings.TrimSuffix(filename, filepath.Ext(filename))

		record := clipDriveRecord{
			ID:           uuid.New().String(),
			Name:         name,
			Filename:     filename,
			FolderPath:   folderPath,
			Group:        folderPath, // Usiamo la cartella come gruppo/categoria
			MediaType:    "clip",
			DownloadLink: "file://" + path,
			Tags:         strings.Split(strings.ToLower(name), " "),
		}

		index.Clips = append(index.Clips, record)
		return nil
	})

	if err != nil {
		log.Fatalf("❌ Errore durante la scansione: %v", err)
	}

	// Scrittura atomica
	tmpFile := *outputFile + ".tmp"
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		log.Fatalf("❌ Errore marshal JSON: %v", err)
	}

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		log.Fatalf("❌ Errore scrittura file temp: %v", err)
	}

	if err := os.Rename(tmpFile, *outputFile); err != nil {
		log.Fatalf("❌ Errore rename file finale: %v", err)
	}

	log.Printf("✅ Indicizzazione completata. %d clip trovate. File aggiornato: %s", len(index.Clips), *outputFile)
}
