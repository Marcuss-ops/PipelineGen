package scriptdocs

import (
	"path/filepath"
	"strings"
	"testing"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
)

func TestBuildMultilingualDocument_UsesStockLinksAndOmitsChapterBlock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stock.db.json")
	db, err := stockdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open stock db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mustUpsertFolder := func(slug, driveID, fullPath, section string) {
		if err := db.UpsertFolder(stockdb.StockFolderEntry{
			TopicSlug: slug,
			DriveID:   driveID,
			FullPath:  fullPath,
			Section:   section,
		}); err != nil {
			t.Fatalf("upsert folder %s: %v", fullPath, err)
		}
	}

	mustUpsertFolder("stock-boxe-tyson", "folder-tyson", "Stock/Boxe/Tyson", "stock")
	mustUpsertFolder("stock-boxe-mayweather", "folder-mayweather", "Stock/Boxe/Mayweather", "stock")
	mustUpsertFolder("stock-artlist-boxing", "1LU5WAfJ1JhCqcYarViMPx7IOEmTnxmPH", "stock/Artlist/boxing", "clips")

	svc := &ScriptDocService{stockDB: db}

	lr := LanguageResult{
		Language: "en",
		FullText: "A long opening chapter about Mike Tyson with enough words to stay in the splitter. A long closing chapter about Floyd Mayweather with enough words to stay in the splitter.",
		Chapters: []ScriptChapter{
			{
				SourceText: "A long opening chapter about Mike Tyson with enough words to stay in the splitter. It keeps the Tyson focus for the first block.",
				StartTime:  0,
				EndTime:    60,
			},
			{
				SourceText: "A long closing chapter about Floyd Mayweather with enough words to stay in the splitter. It keeps the Mayweather focus for the second block.",
				StartTime:  60,
				EndTime:    120,
			},
		},
		Associations: []ClipAssociation{
			{
				Type: "DYNAMIC",
				DynamicClip: &clipsearch.SearchResult{
					Filename: "tyson_knockout.mp4",
					Folder:   "folder-tyson",
				},
			},
			{
				Type: "DYNAMIC",
				DynamicClip: &clipsearch.SearchResult{
					Filename: "boxing_dynamic_boxing.mp4",
					Folder:   "1LU5WAfJ1JhCqcYarViMPx7IOEmTnxmPH",
					Source:   "artlist",
				},
			},
			{
				Type: "STOCK_DB",
				ClipDB: &stockdb.StockClipEntry{
					Filename: "mayweather_defense.mp4",
					FolderID: "folder-mayweather",
					Source:   "stock",
				},
			},
		},
		StockAssociations: []ClipAssociation{
			{
				Type: "STOCK_DB",
				ClipDB: &stockdb.StockClipEntry{
					Filename: "tyson_knockout.mp4",
					FolderID: "folder-tyson",
					Source:   "stock",
				},
			},
			{
				Type: "STOCK_DB",
				ClipDB: &stockdb.StockClipEntry{
					Filename: "mayweather_defense.mp4",
					FolderID: "folder-mayweather",
					Source:   "stock",
				},
			},
		},
		ArtlistAssociations: []ClipAssociation{
			{
				Type: "ARTLIST",
				Clip: &ArtlistClip{
					Name:   "boxing_dynamic_boxing.mp4",
					Term:   "boxing",
					URL:    "https://drive.google.com/file/d/boxing",
					Folder: "stock/Artlist/boxing",
				},
			},
		},
	}

	doc := svc.buildMultilingualDocument(
		"Floyd Mayweather to Mike Tyson",
		120,
		StockFolder{Name: "Stock/Boxe/Floyd", URL: "https://drive.google.com/drive/folders/main"},
		[]LanguageResult{lr},
	)

	if strings.Contains(doc, "📚 CAPITOLI") {
		t.Fatalf("document still contains chapter block:\n%s", doc)
	}
	if !strings.Contains(doc, "📎 STOCK COLLEGATI") {
		t.Fatalf("document does not contain stock links block:\n%s", doc)
	}
	if strings.Contains(doc, "stock/Artlist/boxing") {
		t.Fatalf("document should not include artlist folder in stock block:\n%s", doc)
	}
	if !strings.Contains(doc, "• Inizio: A long opening chapter about Mike Tyson") {
		t.Fatalf("document does not contain the first stock start phrase:\n%s", doc)
	}
	if !strings.Contains(doc, "Fine: It keeps the Tyson focus for the first block") {
		t.Fatalf("document does not contain the first stock end phrase:\n%s", doc)
	}
	if !strings.Contains(doc, "• Inizio: A long closing chapter about Floyd Mayweather") {
		t.Fatalf("document does not contain the second stock start phrase:\n%s", doc)
	}
	if !strings.Contains(doc, "Fine: It keeps the Mayweather focus for the second block") {
		t.Fatalf("document does not contain the second stock end phrase:\n%s", doc)
	}
	if !strings.Contains(doc, "⏱ 0:00 - 1:00") {
		t.Fatalf("document does not contain the first timestamp:\n%s", doc)
	}
	if !strings.Contains(doc, "⏱ 1:00 - 2:00") {
		t.Fatalf("document does not contain the second timestamp:\n%s", doc)
	}
}

func TestBuildMultilingualDocument_MixedMode(t *testing.T) {
	svc := &ScriptDocService{}
	svc.currentAssociationMode = AssociationModeMixed

	doc := svc.buildMultilingualDocument(
		"Canada mountains",
		60,
		StockFolder{Name: "Stock/Clips/Canada", URL: "https://drive.google.com/drive/folders/root"},
		[]LanguageResult{
			{
				Language: "en",
				FullText: "Canada mountains full script.",
				Chapters: []ScriptChapter{
					{StartTime: 0, EndTime: 30, SourceText: "Canada mountains are dramatic and wide."},
				},
				MixedSegments: []MixedSegment{
					{
						ChapterIndex: 1,
						StartTime:    0,
						EndTime:      30,
						Phrase:       "Canada mountains are dramatic and wide.",
						SourceKind:   "image",
						Reason:       "image relevance outranked clip candidates",
						Image: &ImageAssociation{
							Title:    "Canadian Rockies",
							ImageURL: "https://duckduckgo.com/i/d7ae59fa7e82b8e6.jpg",
						},
					},
				},
			},
		},
	)

	if !strings.Contains(doc, "🧩 MIXED (1)") {
		t.Fatalf("document does not contain mixed block:\n%s", doc)
	}
	if !strings.Contains(doc, "Fonte: IMAGE") {
		t.Fatalf("document does not contain image source:\n%s", doc)
	}
	if !strings.Contains(doc, "Canadian Rockies") {
		t.Fatalf("document does not contain image title:\n%s", doc)
	}
	if !strings.Contains(doc, "🔴 CLIP DRIVE (0)") {
		t.Fatalf("document does not contain clip drive placeholder:\n%s", doc)
	}
	if !strings.Contains(doc, "🟢 CLIP ARTLIST (0)") {
		t.Fatalf("document does not contain artlist placeholder:\n%s", doc)
	}
}

func TestBuildMultilingualDocument_ImagesFullModeKeepsStockAndArtlistBlocks(t *testing.T) {
	svc := &ScriptDocService{}
	svc.currentAssociationMode = AssociationModeImagesFull

	doc := svc.buildMultilingualDocument(
		"Andrew Tate",
		45,
		StockFolder{Name: "Stock/Boxe/Andrewtate", URL: "https://drive.google.com/drive/folders/stock"},
		[]LanguageResult{
			{
				Language: "en",
				FullText: "Andrew Tate full script for images.",
				Chapters: []ScriptChapter{
					{StartTime: 0, EndTime: 25, SourceText: "Andrew Tate rose to notoriety through online platforms."},
				},
				ImageAssociations: []ImageAssociation{
					{
						Phrase:    "Andrew Tate rose to notoriety through online platforms.",
						Entity:    "Andrew Tate",
						ImageURL:  "https://duckduckgo.com/i/884ab9a11b153c4e.png",
						Source:    "entityimages",
						StartTime: 0,
						EndTime:   25,
						Score:     1.1,
					},
				},
			},
		},
	)

	if !strings.Contains(doc, "📦 STOCK DRIVE") {
		t.Fatalf("images_full document should contain stock drive block:\n%s", doc)
	}
	if !strings.Contains(doc, "🖼️ IMAGE MODE") {
		t.Fatalf("images_full document should contain image mode block:\n%s", doc)
	}
	if !strings.Contains(doc, "🖼️ IMAGES FULL") {
		t.Fatalf("images_full document should contain images full block:\n%s", doc)
	}
	if !strings.Contains(doc, "Andrew Tate") {
		t.Fatalf("images_full document should contain the image association:\n%s", doc)
	}
	if !strings.Contains(doc, "🔴 CLIP DRIVE (0)") {
		t.Fatalf("images_full document should contain empty clip drive block:\n%s", doc)
	}
	if !strings.Contains(doc, "🟢 CLIP ARTLIST (0)") {
		t.Fatalf("images_full document should contain empty artlist block:\n%s", doc)
	}
}
