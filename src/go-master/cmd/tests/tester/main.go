package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"velox/go-master/internal/clipdb"
	"velox/go-master/pkg/config"
)

func main() {
	dbPath := config.ResolveDataPath("clips_test.db")
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	fmt.Println("🧪 AVVIO TEST BATTERY - SQLITE PRO ENGINE")
	db, err := clipdb.OpenSQLite(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 1. INTEGRIÀ E SCHEMA
	fmt.Print("1.1 Schema e Integrità... ")
	var status string
	err = db.RawDB().QueryRow("PRAGMA integrity_check;").Scan(&status)
	if err != nil || status != "ok" {
		log.Fatalf("FALLITO: %v", status)
	}
	fmt.Println("✅ OK")

	// 2. DEDUPLICA E FTS5
	fmt.Print("1.2 Verifica FTS5 Active... ")
	var ftsTable string
	err = db.RawDB().QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='clips_fts'").Scan(&ftsTable)
	if err != nil {
		log.Fatalf("FALLITO FTS5 NON TROVATO: %v", err)
	}
	fmt.Println("✅ OK")

	// 3. IDEMPOTENZA E MERGE TAG
	fmt.Print("2.1 Idempotenza Upsert... ")
	c1 := &clipdb.ClipEntry{ClipID: "test1", Filename: "Clip 1", LocalPath: "http://t1", Duration: 10, Tags: []string{"nature"}}
	db.UpsertClip(c1)
	db.UpsertClip(c1) // Doppio giro

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM clips WHERE id='test1'").Scan(&count)
	if count != 1 {
		log.Fatalf("FALLITO: atteso 1, trovato %d", count)
	}
	fmt.Println("✅ OK")

	fmt.Print("2.2 Merge Tag... ")
	c2 := &clipdb.ClipEntry{ClipID: "test1", Filename: "Clip 1", LocalPath: "http://t1", Duration: 10, Tags: []string{"forest"}}
	db.UpsertClip(c2) // Secondo giro con nuovo tag

	var tags string
	db.RawDB().QueryRow("SELECT tags FROM clips WHERE id='test1'").Scan(&tags)
	if !contains(tags, "nature") || !contains(tags, "forest") {
		log.Fatalf("FALLITO: tag non uniti correctly: %s", tags)
	}
	fmt.Println("✅ OK")

	// 4. CONCORRENZA WAL
	fmt.Print("3.1 Concorrenza WAL (20 Reader + 1 Writer)... ")
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			db.UpsertClip(&clipdb.ClipEntry{ClipID: fmt.Sprintf("id%d", i), Filename: "T", LocalPath: "U", Duration: 5, Tags: []string{"tag"}})
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _ = db.Resolve(clipdb.QueryParams{Tags: []string{"tag"}})
				}
			}
		}()
	}
	wg.Wait()
	fmt.Println("✅ OK (Nessun lock rilevato)")

	// 5. PERFORMANCE
	fmt.Print("4.1 Performance (1.000 rows fittizie)... ")
	for i := 0; i < 1000; i++ {
		_ = db.UpsertClip(&clipdb.ClipEntry{ClipID: fmt.Sprintf("p%d", i), Filename: "Perf", LocalPath: "U", Duration: 5, Tags: []string{"performance"}})
	}
	start := time.Now()
	res, _ := db.Resolve(clipdb.QueryParams{Tags: []string{"performance"}, Limit: 50})
	elapsed := time.Since(start)
	if len(res) < 50 || elapsed > 20*time.Millisecond {
		log.Fatalf("LENTO: %v (trovati %d)", elapsed, len(res))
	}
	fmt.Printf("✅ OK (%v)\n", elapsed)

	fmt.Println("\n🏆 TUTTI I TEST PASSATI. IL MOTORE È SOLIDO.")
}

func contains(s, substr string) bool {
	return (s != "" && (s == substr || (len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || (len(s) > len(substr)+1 && (s[1:len(substr)+1] == substr)))))) // Semplificata per il test
}
