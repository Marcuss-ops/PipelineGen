package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

type ArtlistClip struct {
	Name     string `json:"name"`
	Term     string `json:"term"`
	URL      string `json:"url"`
	DriveID  string `json:"drive_id"`
	Folder   string `json:"folder"`
	FolderID string `json:"folder_id"`
}

type ArtlistIndex struct {
	Clips []ArtlistClip `json:"clips"`
}

func main() {
	path := "data/artlist_stock_index.json"
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Errore lettura indice: %v", err)
	}

	var idx ArtlistIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		log.Fatalf("Errore parse JSON: %v", err)
	}

	// Raggruppa per termine
	byTerm := make(map[string][]ArtlistClip)
	for _, c := range idx.Clips {
		term := strings.ToLower(strings.TrimSpace(c.Term))
		byTerm[term] = append(byTerm[term], c)
	}

	// Ordina i termini per numero di clip
	var terms []string
	for t := range byTerm {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	fmt.Printf("=== ISPEZIONE DB ARTIST LOCALE (%d clip totali) ===\n\n", len(idx.Clips))

	for _, t := range terms {
		clips := byTerm[t]
		fmt.Printf("📌 TERMINE: [%s] (%d clip)\n", strings.ToUpper(t), len(clips))
		for i, c := range clips {
			if i >= 3 {
				fmt.Printf("   ... e altre %d clip\n", len(clips)-3)
				break
			}
			fmt.Printf("   🔗 %s\n", c.URL)
			fmt.Printf("      Folder: %s | Name: %s\n", c.Folder, c.Name)
		}
		fmt.Println()
	}
}
