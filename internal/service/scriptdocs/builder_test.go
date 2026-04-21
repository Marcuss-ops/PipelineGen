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
