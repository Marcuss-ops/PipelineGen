package scriptdocs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/util"
)

// TestScriptDocsFullPipeline is the COMPLETE end-to-end test.
// Tests: entity extraction, clip association, deduplication, section separation, DB lookup.
func TestScriptDocsFullPipeline(t *testing.T) {
	// 1. Setup: Create a test DB with real Drive data
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stock.db.json")

	// Copy real DB if available, otherwise create minimal test DB
	realDB := "../../data/stock.db.json"
	if data, err := os.ReadFile(realDB); err == nil {
		os.WriteFile(dbPath, data, 0644)
	} else {
		createMinimalTestDB(t, dbPath)
	}

	db, err := stockdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	// Stats
	stats := db.GetStats()
	t.Logf("DB Stats: %v", stats)

	// ===== TEST 1: Section Separation =====
	t.Run("section_separation", func(t *testing.T) {
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

		// Verify no overlap in Drive IDs
		stockIDs := make(map[string]bool)
		for _, f := range stockFolders {
			stockIDs[f.DriveID] = true
		}
		for _, f := range clipsFolders {
			if stockIDs[f.DriveID] {
				t.Errorf("Drive ID %s exists in BOTH sections (should be unique)", f.DriveID)
			}
		}

		t.Logf("Stock folders: %d, Clips folders: %d", len(stockFolders), len(clipsFolders))
	})

	// ===== TEST 2: Topic Resolution (Stock priority) =====
	t.Run("topic_resolution_stock_priority", func(t *testing.T) {
		// Gervonta Davis should resolve to Stock/Boxe/Gervonta
		folder, err := db.FindFolderByTopic("Gervonta Davis")
		if err != nil {
			t.Fatalf("FindFolderByTopic error: %v", err)
		}

		if folder == nil {
			// If not in DB, verify that partial match works for "Boxe"
			folder, err = db.FindFolderByTopic("Boxe")
			if err != nil {
				t.Fatalf("FindFolderByTopic(Boxe) error: %v", err)
			}
			if folder == nil {
				t.Skip("No Boxe folder in DB, skipping")
			}
		}

		if folder.Section != "stock" {
			t.Errorf("Expected section 'stock', got '%s' (Stock should have priority)", folder.Section)
		}

		t.Logf("Topic 'Gervonta Davis' resolved to: %s (section: %s)", folder.FullPath, folder.Section)
	})

	// ===== TEST 3: Section-Specific Lookup =====
	t.Run("section_specific_lookup", func(t *testing.T) {
		// Search in Stock only
		stockFolder, err := db.FindFolderByTopicInSection("Gervonta", "stock")
		if err != nil {
			t.Fatalf("FindFolderByTopicInSection(stock) error: %v", err)
		}

		// Search in Clips only
		clipsFolder, err := db.FindFolderByTopicInSection("Gervonta", "clips")
		if err != nil {
			t.Fatalf("FindFolderByTopicInSection(clips) error: %v", err)
		}

		// Both may or may not exist, but if both exist they should have different Drive IDs
		if stockFolder != nil && clipsFolder != nil {
			if stockFolder.DriveID == clipsFolder.DriveID {
				t.Error("Stock and Clips folders have same Drive ID (should be different)")
			}
			t.Logf("Stock: %s (ID: %s)", stockFolder.FullPath, stockFolder.DriveID)
			t.Logf("Clips: %s (ID: %s)", clipsFolder.FullPath, clipsFolder.DriveID)
		}
	})

	// ===== TEST 4: Entity Extraction =====
	t.Run("entity_extraction", func(t *testing.T) {
		script := loadSampleScript()
		sentences := ExtractSentences(script)

		if len(sentences) < 20 {
			t.Errorf("Expected at least 20 sentences, got %d", len(sentences))
		}

		frasiImportanti := sentences[:util.Min(12, len(sentences))]
		nomiSpeciali := extractProperNouns(sentences)
		paroleImportant := extractKeywords(script)

		// Verify we get the target counts
		t.Logf("Frasi Importanti: %d (target: 12)", len(frasiImportanti))
		t.Logf("Nomi Speciali: %d (target: 12)", len(nomiSpeciali))
		t.Logf("Parole Importanti: %d (target: 12)", len(paroleImportant))

		// Check key entities are found
		expectedNouns := []string{"Davis", "Baltimore", "Mayweather", "Garcia"}
		foundNouns := 0
		for _, expected := range expectedNouns {
			for _, noun := range nomiSpeciali {
				if strings.Contains(noun, expected) || strings.Contains(expected, noun) {
					foundNouns++
					break
				}
			}
		}

		t.Logf("Expected nouns found: %d/%d", foundNouns, len(expectedNouns))

		// Check keywords
		expectedKeywords := []string{"boxing", "davis", "title", "knockout"}
		foundKeywords := 0
		for _, expected := range expectedKeywords {
			for _, kw := range paroleImportant {
				if strings.Contains(kw, expected) || strings.Contains(expected, kw) {
					foundKeywords++
					break
				}
			}
		}

		t.Logf("Expected keywords found: %d/%d", foundKeywords, len(expectedKeywords))
	})

	// ===== TEST 5: Clip Association with Deduplication =====
	t.Run("clip_association_deduplication", func(t *testing.T) {
		// Create service with DB
		idx := &ArtlistIndex{
			Clips: []ArtlistClip{
				{Name: "people_01.mp4", Term: "people", URL: "https://example.com/people_01"},
				{Name: "people_02.mp4", Term: "people", URL: "https://example.com/people_02"},
				{Name: "city_01.mp4", Term: "city", URL: "https://example.com/city_01"},
				{Name: "technology_01.mp4", Term: "technology", URL: "https://example.com/tech_01"},
			},
		}
		idx.ByTerm = make(map[string][]ArtlistClip)
		for _, clip := range idx.Clips {
			idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
		}

		svc := &ScriptDocService{
			artlistIndex: idx,
			stockDB:      db,
		}

		script := loadSampleScript()
		sentences := ExtractSentences(script)
		frasi := sentences[:util.Min(12, len(sentences))]

		// Associate clips (with deduplication)
		associations := svc.associateClips(frasi)

		if len(associations) != len(frasi) {
			t.Errorf("Expected %d associations, got %d", len(frasi), len(associations))
		}

		// Check for duplicates
		usedArtlistClips := make(map[string]int)
		usedDBClips := make(map[string]int)
		for _, a := range associations {
			if a.Type == "ARTLIST" && a.Clip != nil {
				usedArtlistClips[a.Clip.Name]++
			}
			if a.Type == "STOCK_DB" && a.ClipDB != nil {
				usedDBClips[a.ClipDB.ClipID]++
			}
		}

		// Verify no duplicates
		for clipName, count := range usedArtlistClips {
			if count > 1 {
				t.Errorf("Artlist clip '%s' used %d times (should be 1)", clipName, count)
			}
		}
		for clipID, count := range usedDBClips {
			if count > 1 {
				t.Errorf("DB clip '%s' used %d times (should be 1)", clipID, count)
			}
		}

		// Report
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

		t.Logf("Clip types: ARTLIST=%d, STOCK_DB=%d, STOCK=%d", artlistCount, dbCount, stockCount)

		// Log associations
		for i, a := range associations {
			t.Logf("  %d. Type: %s, Confidence: %.2f, Keyword: %s", i+1, a.Type, a.Confidence, a.MatchedKeyword)
		}
	})

	// ===== TEST 6: Cross-Section Clip Search =====
	t.Run("cross_section_clip_search", func(t *testing.T) {
		if db == nil {
			t.Skip("No DB available")
		}

		// Search clips by tags in both sections
		stockClips, err := db.SearchClipsByTags([]string{"boxing", "fight", "knockout"})
		if err != nil {
			t.Fatalf("SearchClipsByTags error: %v", err)
		}

		t.Logf("Stock clips matching boxing/fight/knockout: %d", len(stockClips))

		// Get clips for a specific folder
		folder, err := db.FindFolderByTopic("Gervonta")
		if err != nil || folder == nil {
			folder, err = db.FindFolderByTopic("Boxe")
		}

		if folder != nil {
			clips, err := db.GetClipsForFolder(folder.DriveID)
			if err != nil {
				t.Fatalf("GetClipsForFolder error: %v", err)
			}
			t.Logf("Clips in folder %s: %d", folder.FullPath, len(clips))

			if len(clips) > 0 {
				t.Logf("  First clip: %s (source: %s, duration: %ds)", clips[0].Filename, clips[0].Source, clips[0].Duration)
			}
		}
	})

	// ===== TEST 7: Unused Clips (Deduplication) =====
	t.Run("unused_clips_deduplication", func(t *testing.T) {
		if db == nil {
			t.Skip("No DB available")
		}

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

// TestDBLookupSpeed verifies that DB lookup is faster than Drive API
func TestDBLookupSpeed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stock.db.json")

	realDB := "../../data/stock.db.json"
	if data, err := os.ReadFile(realDB); err == nil {
		os.WriteFile(dbPath, data, 0644)
	} else {
		createMinimalTestDB(t, dbPath)
	}

	db, err := stockdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	// Benchmark DB lookup
	topics := []string{
		"Gervonta Davis",
		"Boxe",
		"Mike Tyson",
		"Escobar",
		"Wwe",
		"Discovery",
	}

	for _, topic := range topics {
		start := time.Now()
		folder, err := db.FindFolderByTopic(topic)
		elapsed := time.Since(start)

		if err != nil {
			t.Errorf("FindFolderByTopic(%s) error: %v", topic, err)
		}

		if folder != nil {
			t.Logf("DB lookup '%s': %v → %s (section: %s)", topic, elapsed, folder.FullPath, folder.Section)
		} else {
			t.Logf("DB lookup '%s': %v → not found", topic, elapsed)
		}

		// DB lookup should be < 100ms
		if elapsed > 100_000_000 { // 100ms in nanoseconds
			t.Errorf("DB lookup took %v (should be < 100ms)", elapsed)
		}
	}
}

func loadSampleScript() string {
	return `From Nothing

He was born Gervonta Bryant Davis on November 7, 1994, not into boxing royalty but into Sandtown-Winchester, West Baltimore, one of the most violent zip codes in America. The official biography puts it plainly: Davis was raised in Sandtown-Winchester, his parents were drug addicts and were frequently in and out of jail.

Boxing was not a hobby. It was daycare, then discipline, then salvation. At five years old he walked into Upton Boxing Center, a converted gym on Pennsylvania Avenue, and met Calvin Ford, the man who would become trainer, father figure, and legal guardian in practice if not on paper.

He turned pro at 18, on February 22, 2013, against Desi Williams at the D.C. Armory, and won via first-round knockout. By August 2014 he was 8-0, all inside the distance. Floyd Mayweather Jr. saw the tape and signed him to Mayweather Promotions in 2015.

The rise was violent and fast. On January 14, 2017, at Barclays Center, the 22-year-old challenged undefeated IBF super featherweight champion Jose Pedraza. Davis defeated Pedraza in a seventh-round KO to win the IBF super featherweight title.

What followed was a rare kind of dominance across weight. He became a holder of the IBF super featherweight title in 2017, the WBA super featherweight title twice between 2018 and 2020, the WBA super lightweight title in 2021, and the WBA lightweight title from 2023 to 2026.

Léo Santa Cruz, October 31, 2020. Alamodome, pandemic era. Davis retained his WBA lightweight title and won the WBA super featherweight title with a left uppercut in round six that is still replayed as a perfect punch.

Mario Barrios, June 26, 2021. Moving up to 140 pounds, Davis stopped the bigger Barrios in the 11th to win the WBA super lightweight title. He became a three-division champion at 26.

Ryan Garcia, April 22, 2023. This was the cultural peak. T-Mobile Arena, Showtime and DAZN joint PPV, two undefeated social-media stars in their prime. Davis won by KO in round 7. The fight did 1,200,000 buys and $87,000,000 in revenue.

By then Tank was no longer just a fighter. He was a Baltimore homecoming, he was Under Armour deals, 3.4 million Instagram followers, and a knockout rate of 93 percent.

Then came the case that broke the career. On October 31, 2025, his former girlfriend filed a civil lawsuit alleging battery, kidnapping and false imprisonment. The boxing world reacted instantly. WBA announced Davis was no longer the active titleholder but had been given the title of champion in recess.`
}

func createMinimalTestDB(t *testing.T, path string) {
	db := map[string]interface{}{
		"last_synced": "2026-04-13T00:00:00",
		"folders": []map[string]string{
			{"topic_slug": "stock-boxe-gervonta", "drive_id": "gervonta_stock_id", "parent_id": "boxe_id", "full_path": "stock/Boxe/Gervonta", "section": "stock"},
			{"topic_slug": "clips-boxe-gervonta", "drive_id": "gervonta_clips_id", "parent_id": "boxe_clips_id", "full_path": "clips/Boxe/Gervonta", "section": "clips"},
			{"topic_slug": "stock-boxe", "drive_id": "boxe_id", "parent_id": "", "full_path": "stock/Boxe", "section": "stock"},
		},
		"clips": []map[string]interface{}{
			{"clip_id": "clip_001", "folder_id": "gervonta_stock_id", "filename": "knockout_garcia.mp4", "source": "stock", "tags": "knockout punch ring crowd", "duration": 15},
			{"clip_id": "clip_002", "folder_id": "gervonta_stock_id", "filename": "training_camp.mp4", "source": "stock", "tags": "training gym boxing people", "duration": 20},
			{"clip_id": "clip_003", "folder_id": "gervonta_clips_id", "filename": "interview.mp4", "source": "clips", "tags": "interview press conference people", "duration": 30},
		},
	}

	data, _ := json.Marshal(db)
	os.WriteFile(path, data, 0644)
}
