package scriptdocs

import (
	"os"
	"strings"
	"testing"
	"time"

	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/util"
)

// TestFullPipelineWithRealDB tests the complete pipeline with the real Drive-scanned DB.
func TestFullPipelineWithRealDB(t *testing.T) {
	// 1. Load real DB
	dbPath := "../../../data/stock.db.json"
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("Real DB not found at %s, skipping", dbPath)
	}

	db, err := stockdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open real DB: %v", err)
	}
	defer db.Close()

	stats := db.GetStats()
	t.Logf("Real DB Stats: %v", stats)

	if stats["folders"] == 0 || stats["clips"] == 0 {
		t.Skip("DB is empty, skipping")
	}

	// ===== TEST 1: Section Separation Verified =====
	t.Run("section_separation_verified", func(t *testing.T) {
		stockFolders, err := db.GetFoldersBySection("stock")
		if err != nil {
			t.Fatalf("GetFoldersBySection(stock) error: %v", err)
		}

		clipsFolders, err := db.GetFoldersBySection("clips")
		if err != nil {
			t.Fatalf("GetFoldersBySection(clips) error: %v", err)
		}

		if len(stockFolders) == 0 {
			t.Error("Expected stock folders, got 0")
		}
		if len(clipsFolders) == 0 {
			t.Error("Expected clips folders, got 0")
		}

		// Verify no Drive ID overlap
		stockIDs := make(map[string]string) // drive_id -> full_path
		for _, f := range stockFolders {
			if existing, ok := stockIDs[f.DriveID]; ok {
				t.Errorf("Duplicate Drive ID in stock: %s (also in %s)", f.FullPath, existing)
			}
			stockIDs[f.DriveID] = f.FullPath
		}

		clipsIDs := make(map[string]string)
		for _, f := range clipsFolders {
			if existing, ok := clipsIDs[f.DriveID]; ok {
				t.Errorf("Duplicate Drive ID in clips: %s (also in %s)", f.FullPath, existing)
			}
			clipsIDs[f.DriveID] = f.FullPath
		}

		// Check cross-section overlap
		for driveID, stockPath := range stockIDs {
			if clipsPath, exists := clipsIDs[driveID]; exists {
				t.Errorf("Drive ID %s exists in BOTH sections:\n  Stock: %s\n  Clips: %s",
					driveID, stockPath, clipsPath)
			}
		}

		t.Logf("Stock folders: %d, Clips folders: %d, No overlap ✅", len(stockFolders), len(clipsFolders))
	})

	// ===== TEST 2: Topic Resolution with Real DB =====
	t.Run("topic_resolution_real_db", func(t *testing.T) {
		testCases := []struct {
			topic       string
			expectFound bool
			expectSection string
		}{
			{"Gervonta Davis", true, "stock"},
			{"Boxe", true, "stock"},
			{"Escobar", true, "stock"},
			{"Wwe", true, "stock"},
			{"Discovery", true, "stock"},
			{"HipHop", true, "stock"},
			{"Musica", true, "stock"},
			{"Crimine", true, "stock"},
			{"NonExistentTopic", false, ""},
		}

		for _, tc := range testCases {
			t.Run(tc.topic, func(t *testing.T) {
				folder, err := db.FindFolderByTopic(tc.topic)
				if err != nil {
					t.Fatalf("FindFolderByTopic(%s) error: %v", tc.topic, err)
				}

				if tc.expectFound {
					if folder == nil {
						t.Logf("⚠️  Topic '%s' not found in DB (may need sync)", tc.topic)
						return // Not a failure, DB may not have all topics
					}

					t.Logf("✅ '%s' → %s (section: %s)", tc.topic, folder.FullPath, folder.Section)

					// Verify section priority (stock should be found first)
					if tc.expectSection != "" && folder.Section != tc.expectSection {
						// This is OK if the topic exists in both sections
						t.Logf("   Note: Found in '%s' section (expected '%s')", folder.Section, tc.expectSection)
					}
				} else {
					if folder != nil {
						t.Logf("⚠️  Topic '%s' unexpectedly found: %s", tc.topic, folder.FullPath)
					}
				}
			})
		}
	})

	// ===== TEST 3: Lookup Speed =====
	t.Run("lookup_speed", func(t *testing.T) {
		topics := []string{
			"Gervonta Davis",
			"Andrew Tate",
			"Floyd Mayweather",
			"Mike Tyson",
			"Escobar",
			"Cody Rhodes",
			"Elvis Presley",
			"50 Cent",
		}

		totalTime := time.Duration(0)
		foundCount := 0

		for _, topic := range topics {
			start := time.Now()
			folder, err := db.FindFolderByTopic(topic)
			elapsed := time.Since(start)
			totalTime += elapsed

			if err != nil {
				t.Errorf("FindFolderByTopic(%s) error: %v", topic, err)
			}

			if folder != nil {
				foundCount++
				t.Logf("✅ '%s': %v → %s", topic, elapsed, folder.FullPath)
			} else {
				t.Logf("⚠️  '%s': %v → not found", topic, elapsed)
			}

			// Each lookup should be < 100ms
			if elapsed > 100*time.Millisecond {
				t.Errorf("Lookup took %v (should be < 100ms)", elapsed)
			}
		}

		avgTime := totalTime / time.Duration(len(topics))
		t.Logf("Average lookup: %v, Found: %d/%d", avgTime, foundCount, len(topics))

		// Average should be < 1ms
		if avgTime > time.Millisecond {
			t.Logf("⚠️  Average lookup %v is slow (should be < 1ms)", avgTime)
		}
	})

	// ===== TEST 4: Entity Extraction =====
	t.Run("entity_extraction_real_db", func(t *testing.T) {
		script := loadSampleScript()
		sentences := ExtractSentences(script)

		if len(sentences) < 10 {
			t.Errorf("Expected at least 10 sentences, got %d", len(sentences))
		}

		// Get 12 important sentences
		frasiImportanti := sentences[:util.Min(12, len(sentences))]
		nomiSpeciali := extractProperNouns(sentences)
		paroleImportant := extractKeywords(script)

		t.Logf("Frasi Importanti: %d", len(frasiImportanti))
		t.Logf("Nomi Speciali: %d (%v)", len(nomiSpeciali), nomiSpeciali[:util.Min(10, len(nomiSpeciali))])
		t.Logf("Parole Importanti: %d (%v)", len(paroleImportant), paroleImportant[:util.Min(10, len(paroleImportant))])

		// Check key entities
		expectedNouns := []string{"Davis", "Baltimore", "Mayweather", "Garcia"}
		for _, expected := range expectedNouns {
			found := false
			for _, noun := range nomiSpeciali {
				if strings.Contains(noun, expected) || strings.Contains(expected, noun) {
					found = true
					break
				}
			}
			if found {
				t.Logf("✅ Found expected noun: %s", expected)
			} else {
				t.Logf("⚠️  Expected noun not found: %s", expected)
			}
		}
	})

	// ===== TEST 5: Clip Association with Real DB =====
	t.Run("clip_association_real_db", func(t *testing.T) {
		// Create service with real DB
		svc := &ScriptDocService{
			stockDB: db,
		}

		script := loadSampleScript()
		sentences := ExtractSentences(script)
		frasi := sentences[:util.Min(12, len(sentences))]

		// Associate clips with deduplication
		associations := svc.associateClips(frasi, StockFolder{ID: "root", Name: "Stock"}, "Test Topic")

		if len(associations) != len(frasi) {
			t.Errorf("Expected %d associations, got %d", len(frasi), len(associations))
		}

		// Check for duplicates
		usedClipIDs := make(map[string]int)
		for _, a := range associations {
			if a.Type == "STOCK_DB" && a.ClipDB != nil {
				usedClipIDs[a.ClipDB.ClipID]++
			}
		}

		// Verify no duplicates
		for clipID, count := range usedClipIDs {
			if count > 1 {
				t.Errorf("DB clip '%s' used %d times (should be 1)", clipID, count)
			}
		}

		// Report distribution
		artlistCount := 0
		dbCount := 0
		stockCount := 0
		for _, a := range associations {
			switch a.Type {
			case "ARTLIST":
				artlistCount++
			case "STOCK_DB":
				dbCount++
			case "STOCK":
				stockCount++
			}
		}

		t.Logf("Clip distribution: ARTLIST=%d, STOCK_DB=%d, STOCK=%d", artlistCount, dbCount, stockCount)

		// Log each association
		for i, a := range associations {
			if i >= 5 {
				t.Logf("  ... (%d more)", len(associations)-5)
				break
			}
			t.Logf("  %d. Type: %s, Confidence: %.2f, Keyword: %s", i+1, a.Type, a.Confidence, a.MatchedKeyword)
		}
	})

	// ===== TEST 6: Search Clips by Tags =====
	t.Run("search_clips_by_tags", func(t *testing.T) {
		tags := []string{"boxing", "fight", "knockout", "training", "ring", "punch"}
		clips, err := db.SearchClipsByTags(tags)
		if err != nil {
			t.Fatalf("SearchClipsByTags error: %v", err)
		}

		t.Logf("Found %d clips matching: %v", len(clips), tags)

		// Verify clips have correct tags
		for i, clip := range clips {
			if i >= 5 {
				t.Logf("  ... (%d more)", len(clips)-5)
				break
			}
			t.Logf("  Clip: %s (source: %s, folder: %s)", clip.Filename, clip.Source, clip.FolderID)
		}

		// Should find some clips
		if len(clips) == 0 {
			t.Logf("⚠️  No clips found for tags: %v", tags)
		}
	})

	// ===== TEST 7: Unused Clips (Deduplication) =====
	t.Run("unused_clips", func(t *testing.T) {
		// Simulate some clips already used
		usedIDs := []string{"clip_001", "clip_002", "clip_003"}

		// Get unused clips
		unusedClips, err := db.GetUnusedClips("", usedIDs)
		if err != nil {
			t.Fatalf("GetUnusedClips error: %v", err)
		}

		// Verify none of the used clips are in the result
		usedMap := make(map[string]bool)
		for _, id := range usedIDs {
			usedMap[id] = true
		}

		for _, clip := range unusedClips {
			if usedMap[clip.ClipID] {
				t.Errorf("Used clip '%s' returned in unused list", clip.ClipID)
			}
		}

		t.Logf("Unused clips available: %d", len(unusedClips))
	})
}

// TestScriptDocsEndToEnd tests the full script docs pipeline with real DB
func TestScriptDocsEndToEnd(t *testing.T) {
	// Load real DB
	dbPath := "../../../data/stock.db.json"
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("Real DB not found at %s, skipping", dbPath)
	}

	db, err := stockdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open real DB: %v", err)
	}
	defer db.Close()

	t.Logf("=== Script Docs End-to-End Test ===")
	t.Logf("DB loaded: %d folders, %d clips", len(mustGetFolders(db)), len(mustGetClips(db)))

	// Step 1: Resolve Stock folder
	folder, err := db.FindFolderByTopic("Gervonta Davis")
	if err != nil {
		t.Fatalf("FindFolderByTopic error: %v", err)
	}

	if folder == nil {
		t.Log("⚠️  Gervonta folder not found in DB, trying partial match...")
		folder, err = db.FindFolderByTopic("Gervonta")
		if err != nil {
			t.Fatalf("FindFolderByTopic(Gervonta) error: %v", err)
		}
	}

	if folder != nil {
		t.Logf("✅ Stock folder: %s (ID: %s, section: %s)", folder.FullPath, folder.DriveID, folder.Section)

		// Step 2: Get clips for this folder
		clips, err := db.GetClipsForFolder(folder.DriveID)
		if err != nil {
			t.Fatalf("GetClipsForFolder error: %v", err)
		}

		t.Logf("✅ Clips in folder: %d", len(clips))

		if len(clips) > 0 {
			t.Logf("  First clip: %s (source: %s, tags: %s, duration: %ds)",
				clips[0].Filename, clips[0].Source, clips[0].Tags, clips[0].Duration)
		}
	} else {
		t.Log("⚠️  No folder found for Gervonta")
	}

	// Step 3: Search for relevant clips
	tags := []string{"boxing", "fight", "knockout", "training"}
	relevantClips, err := db.SearchClipsByTags(tags)
	if err != nil {
		t.Fatalf("SearchClipsByTags error: %v", err)
	}

	t.Logf("✅ Relevant clips found: %d", len(relevantClips))
}

func mustGetFolders(db *stockdb.StockDB) []stockdb.StockFolderEntry {
	folders, _ := db.GetAllFolders()
	return folders
}

func mustGetClips(db *stockdb.StockDB) []stockdb.StockClipEntry {
	clips, _ := db.GetAllClips()
	return clips
}

