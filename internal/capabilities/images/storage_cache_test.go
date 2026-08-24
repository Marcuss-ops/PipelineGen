package images

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"go.uber.org/zap"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stubRetrievalProvider struct {
	name asset.ImageProvider
}

func (p stubRetrievalProvider) Search(_ context.Context, _ string, _ routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	return nil, nil
}

func (p stubRetrievalProvider) Name() asset.ImageProvider { return p.name }

func (p stubRetrievalProvider) Healthy(context.Context) error { return nil }

func openImageCacheTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE subjects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'image',
			name TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]',
			tags_norm TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT 'image',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			relative_path TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'STAGING',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			origin TEXT NOT NULL DEFAULT 'retrieved',
			provider TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("apply schema: %v\nstmt=%s", err, stmt)
		}
	}
	return db
}

func seedCachedImage(t *testing.T, db *sql.DB, id, hash, sourceQuery string, createdAt time.Time) {
	t.Helper()
	metadata := `{"subject_id":"jaguar","source_query":"` + sourceQuery + `","resolved_query":"` + sourceQuery + `"}`
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO media_assets (
			id, source, name, url, tags, legacy_file_md5, local_path, relative_path,
			drive_file_id, drive_link, metadata_json, origin, provider, created_at, updated_at
		) VALUES (?, 'image', ?, ?, ?, ?, ?, ?, ?, ?, ?, 'retrieved', ?, ?, ?)
	`,
		id,
		"Image for "+sourceQuery,
		"https://example.invalid/"+id+".jpg",
		`["jaguar","animal"]`,
		hash,
		"/tmp/"+id+".jpg",
		"relative/"+id+".jpg",
		"",
		"",
		metadata,
		string(asset.ProviderWikipedia),
		createdAt.UTC().Format(time.RFC3339),
		createdAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("seed cached image %s: %v", id, err)
	}
}

func seedSubject(t *testing.T, db *sql.DB, slug string) {
	t.Helper()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO subjects (id, name, description, metadata_json, created_at, updated_at)
		VALUES (?, ?, '', '{}', ?, ?)
	`, slug, slug, now, now)
	if err != nil {
		t.Fatalf("seed subject %s: %v", slug, err)
	}
}

func TestImageSearchCacheKey_NormalizesQueryLanguageAndPolicy(t *testing.T) {
	base := imageSearchCacheKey(" Jaguar   animal in rainforest ", " IT ", "wikipedia,searxng,duckduckgo,drive")
	same := imageSearchCacheKey("jaguar animal in rainforest", "it", "wikipedia,searxng,duckduckgo,drive")
	if base != same {
		t.Fatalf("normalized keys differ: %q vs %q", base, same)
	}

	otherQuery := imageSearchCacheKey("jaguar luxury car", "it", "wikipedia,searxng,duckduckgo,drive")
	if base == otherQuery {
		t.Fatalf("different queries must not share a cache key: %q", base)
	}

	otherLang := imageSearchCacheKey("jaguar animal in rainforest", "en", "wikipedia,searxng,duckduckgo,drive")
	if base == otherLang {
		t.Fatalf("different languages must not share a cache key: %q", base)
	}

	otherPolicy := imageSearchCacheKey("jaguar animal in rainforest", "it", "drive")
	if base == otherPolicy {
		t.Fatalf("different provider policies must not share a cache key: %q", base)
	}
}

func TestRetrievalPolicySignature_ReflectsProviderOrder(t *testing.T) {
	svc := &ImageStorageService{
		retrievalRegistry: retrieved.NewRetrievalProviderRegistry(zap.NewNop(), []retrieved.RetrievalProvider{
			stubRetrievalProvider{name: asset.ProviderWikipedia},
			stubRetrievalProvider{name: asset.ProviderDrive},
		}),
	}
	if got := svc.retrievalPolicySignature(); got != "wikipedia,drive" {
		t.Fatalf("retrievalPolicySignature() = %q, want %q", got, "wikipedia,drive")
	}
}

func TestSearchAndDownload_UsesSemanticCacheHitAndAvoidsCollision(t *testing.T) {
	db := openImageCacheTestDB(t)
	repo := imagesrepo.NewImagesRepository(db)
	seedSubject(t, db, "jaguar")

	seedCachedImage(t, db, "img-car", "hash-car", "jaguar luxury car", time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	seedCachedImage(t, db, "img-animal", "hash-animal", "jaguar animal in rainforest", time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC))

	if subject, err := repo.GetSubjectBySlugOrAlias(context.Background(), "jaguar"); err != nil {
		t.Fatalf("GetSubjectBySlugOrAlias preflight: %v", err)
	} else if subject == nil {
		t.Fatal("GetSubjectBySlugOrAlias preflight returned nil subject")
	}

	if got, err := repo.ListImagesBySubject(context.Background(), "jaguar"); err != nil {
		t.Fatalf("ListImagesBySubject preflight: %v", err)
	} else if len(got) != 2 {
		t.Fatalf("ListImagesBySubject preflight returned %d rows, want 2", len(got))
	} else {
		if cached, score := selectBestCachedImageAsset("jaguar animal in rainforest", got); cached == nil {
			t.Fatalf("selectBestCachedImageAsset preflight returned nil on %d rows", len(got))
		} else if cached.Hash != "hash-animal" || score < minCachedImageScore {
			t.Fatalf("selectBestCachedImageAsset preflight picked hash=%s score=%d, want hash-animal with strong match", cached.Hash, score)
		}
	}

	svc := &ImageStorageService{
		repo: repo,
		log:  zap.NewNop(),
		client: &http.Client{
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("unexpected network call during cache-hit test")
			}),
		},
		retrievalRegistry: retrieved.NewRetrievalProviderRegistry(zap.NewNop(), []retrieved.RetrievalProvider{
			stubRetrievalProvider{name: asset.ProviderWikipedia},
		}),
	}

	animal1, err := svc.SearchAndDownloadDetailed(context.Background(), "jaguar", "Jaguar", "jaguar animal in rainforest", "it", nil)
	if err != nil {
		t.Fatalf("SearchAndDownloadDetailed(animal) first run: %v", err)
	}
	if animal1 == nil || animal1.Asset == nil {
		t.Fatal("animal query returned nil detailed result")
	}
	if !animal1.CacheHit || animal1.CacheSource != "database" || animal1.RetrievalProvider != string(asset.ProviderWikipedia) {
		t.Fatalf("animal detailed cache trace wrong: %+v", animal1)
	}
	if animal1.Asset.Hash != "hash-animal" {
		t.Fatalf("animal query returned hash %q, want %q", animal1.Asset.Hash, "hash-animal")
	}

	animal2, err := svc.SearchAndDownloadDetailed(context.Background(), "jaguar", "Jaguar", "jaguar animal in rainforest", "it", nil)
	if err != nil {
		t.Fatalf("SearchAndDownloadDetailed(animal) second run: %v", err)
	}
	if animal2 == nil || animal2.Asset == nil {
		t.Fatal("animal replay returned nil detailed result")
	}
	if !animal2.CacheHit || animal2.CacheSource != animal1.CacheSource || animal2.RetrievalProvider != animal1.RetrievalProvider {
		t.Fatalf("animal replay cache trace changed: first=%+v second=%+v", animal1, animal2)
	}
	if animal2.Asset.Hash != animal1.Asset.Hash {
		t.Fatalf("replay must return same hash: got %q, want %q", animal2.Asset.Hash, animal1.Asset.Hash)
	}

	car, err := svc.SearchAndDownloadDetailed(context.Background(), "jaguar", "Jaguar", "jaguar luxury car", "it", nil)
	if err != nil {
		t.Fatalf("SearchAndDownloadDetailed(car): %v", err)
	}
	if car == nil || car.Asset == nil {
		t.Fatal("car query returned nil detailed result")
	}
	if !car.CacheHit || car.CacheSource != "database" || car.RetrievalProvider != string(asset.ProviderWikipedia) {
		t.Fatalf("car detailed cache trace wrong: %+v", car)
	}
	if car.Asset.Hash != "hash-car" {
		t.Fatalf("car query returned hash %q, want %q", car.Asset.Hash, "hash-car")
	}

	if car.Asset.Hash == animal1.Asset.Hash {
		t.Fatalf("semantic anti-collision failed: both queries returned %q", car.Asset.Hash)
	}
}
