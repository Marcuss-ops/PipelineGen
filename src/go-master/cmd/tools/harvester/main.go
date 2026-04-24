package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"velox/go-master/internal/artlist"
	"velox/go-master/internal/clipdb"
	"velox/go-master/pkg/config"
)

func main() {
	keyword := flag.String("keyword", "born", "La keyword da cercare su Artlist")
	max := flag.Int("max", 20, "Numero massimo di clip da trovare")
	autoSave := flag.Int("auto-save", 0, "Salva automaticamente i primi N link trovati nel DB")
	flag.Parse()

	if *keyword == "" {
		log.Fatal("La keyword non può essere vuota")
	}

	scraperPath := "../node-scraper/scripts/scrape_artlist_discovery.js"
	outputDir := config.ResolveDataPath("async_pipeline_jobs")
	dbPath := config.ResolveDataPath("clips_catalog.db")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Errore creazione cartella output: %v", err)
	}

	harvester := artlist.NewHarvester(scraperPath, outputDir)

	fmt.Printf("🚀 Avvio Discovery su Artlist per: [%s]\n", *keyword)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := harvester.Harvest(ctx, *keyword, *max)
	if err != nil {
		log.Fatalf("❌ Errore durante la raccolta: %v", err)
	}

	stagingPath, err := harvester.SaveToStaging(result)
	if err != nil {
		log.Fatalf("❌ Errore durante il salvataggio: %v", err)
	}

	fmt.Printf("\n✅ Ricerca completata! Trovate %d clip (nel buffer: %d).\n", result.Count, len(result.Clips))
	fmt.Printf("📂 Risultati salvati in: %s\n", stagingPath)

	db, err := clipdb.OpenSQLite(dbPath)
	if err != nil {
		log.Fatalf("❌ Errore apertura database SQLite: %v", err)
	}
	defer db.Close()

	fmt.Println("\n--- SALVATAGGIO LINK NEL DATABASE SQLITE ---")
	savedCount := 0
	for i, c := range result.Clips {
		if savedCount >= 20 {
			break
		}

		if *autoSave > 0 && savedCount < *autoSave {
			entry := &clipdb.ClipEntry{
				ClipID:    c.ID,
				Filename:  c.Title,
				Source:    "artlist",
				LocalPath: c.Mp4URL, // URL diretto dell'MP4
				Duration:  c.Duration / 1000,
				Tags:      []string{*keyword},
			}
			if err := db.UpsertClip(entry); err == nil {
				fmt.Printf("✅ [%d/%d] Link salvato in SQLite: %s\n", i+1, len(result.Clips), c.Title)
				savedCount++
			} else {
				fmt.Printf("❌ Errore salvataggio: %v\n", err)
			}
		} else {
			fmt.Printf("%d. [%s]\n   🔗 MP4: %s\n", i+1, c.Title, c.Mp4URL)
		}
	}

	fmt.Printf("\n🎉 Sessione terminata. Salvati %d nuovi link per la tag [%s].\n", savedCount, *keyword)
}
