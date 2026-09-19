package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	gdrive "google.golang.org/api/drive/v3"
)

const elonMuskOverlayDriveRoot = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"

var elonMuskOverlayLanguages = []string{"en", "it", "de", "fr", "es", "pt-BR", "pl", "ru", "tr", "id"}

// TestLiveElonMuskOverlayDriveRuntime is deliberately separate from the
// property-only Elon test. It enables voiceover and semantic overlay rendering
// without image materialization, so it closes the phrase-only path first:
// ten languages, the global five-phrase editorial budget, five certified
// per-item artifacts per language, and Drive publication identity.
func TestLiveElonMuskOverlayDriveRuntime(t *testing.T) {
	if os.Getenv("PIPELINEGEN_ELON_MUSK_OVERLAY_LIVE") != "1" || os.Getenv("PIPELINEGEN_ELON_MUSK_OVERLAY_REAL_DRIVE") != "1" {
		t.Skip("set PIPELINEGEN_ELON_MUSK_OVERLAY_LIVE=1 and PIPELINEGEN_ELON_MUSK_OVERLAY_REAL_DRIVE=1 to run the live Elon Musk overlay/Drive certification")
	}
	token := liveAdminToken()
	if token == "" {
		t.Skip("VELOX_ADMIN_TOKEN or TOKEN_FILE is required for the live Elon Musk overlay test")
	}

	body := readElonMuskOverlayFixture(t)
	assertNoPhraseOracle(t, body)
	baseURL := strings.TrimRight(getenv("VELOX_API_BASE_URL", "http://127.0.0.1:8000"), "/")
	client := &http.Client{}
	jobID := submitMikeTysonRuntime(t, client, baseURL, token, body)
	full := waitForMikeTysonRuntime(t, client, baseURL, token, jobID)
	result := generationResult(full)
	if result == nil {
		t.Fatalf("job %s completed without job.result.result", jobID)
	}
	verifyElonMuskOverlayResult(t, result, jobID)
	t.Logf("overlay runtime PASS: job=%s languages=%d artifacts=%d", jobID, len(elonMuskOverlayLanguages), len(elonMuskOverlayLanguages)*5)
}

func readElonMuskOverlayFixture(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while resolving Elon Musk overlay fixture")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "ops", "jobs", "elon_musk_1000w_overlay.generate.json"))
	if err != nil {
		t.Fatalf("read Elon Musk overlay fixture: %v", err)
	}
	if !json.Valid(body) {
		t.Fatal("Elon Musk overlay fixture is not valid JSON")
	}
	return body
}

func verifyElonMuskOverlayResult(t *testing.T, result map[string]any, jobID string) {
	t.Helper()
	if len(mapsAt(result, "scenes")) != 5 {
		t.Fatalf("scenes=%d, want five", len(mapsAt(result, "scenes")))
	}
	if integerAt(mapAt(result, "output"), "word_count") < 850 {
		t.Fatalf("word_count=%d, want approximately 1000", integerAt(mapAt(result, "output"), "word_count"))
	}
	if stringAt(result, "source_language") != "en" {
		t.Fatalf("source_language=%q, want en", stringAt(result, "source_language"))
	}

	plans := mapAt(result, "localized_overlay_plans")
	if len(plans) != len(elonMuskOverlayLanguages)-1 {
		t.Fatalf("localized overlay plans=%d, want nine", len(plans))
	}
	if sourcePlan := mapAt(result, "overlay_plan"); sourcePlan == nil {
		t.Fatal("source overlay_plan is missing")
	} else {
		verifyFivePhrasePlan(t, "en", sourcePlan)
	}
	for _, language := range elonMuskOverlayLanguages[1:] {
		plan := mapAt(plans, language)
		if plan == nil {
			t.Fatalf("overlay plan for %s is missing", language)
		}
		verifyFivePhrasePlan(t, language, plan)
	}

	seenFolders := make(map[string]string, len(elonMuskOverlayLanguages))
	verifyOverlayReference(t, "en", mapAt(result, "overlay_render"), jobID, seenFolders)
	renders := mapAt(result, "localized_overlay_renders")
	if len(renders) != len(elonMuskOverlayLanguages)-1 {
		t.Fatalf("localized overlay renders=%d, want nine", len(renders))
	}
	for _, language := range elonMuskOverlayLanguages[1:] {
		verifyOverlayReference(t, language, mapAt(renders, language), jobID, seenFolders)
	}
	if len(seenFolders) != len(elonMuskOverlayLanguages) {
		t.Fatalf("distinct Drive folders=%d, want ten (one per language)", len(seenFolders))
	}
	verifyElonMuskOverlayDriveTree(t, result, jobID)
}

func verifyFivePhrasePlan(t *testing.T, language string, plan map[string]any) {
	t.Helper()
	if got := stringAt(plan, "language"); got != language {
		t.Fatalf("%s plan language=%q, want %q", language, got, language)
	}
	items := mapsAt(plan, "items")
	phraseCount := 0
	for _, item := range items {
		if stringAt(item, "kind") == "text_phrase" {
			phraseCount++
			start, end := integerAt(item, "start_ms"), integerAt(item, "end_ms")
			if start < 0 || end <= start {
				t.Fatalf("%s phrase %q has invalid timing [%d,%d]", language, stringAt(item, "text"), start, end)
			}
		}
	}
	if phraseCount != 5 {
		t.Fatalf("%s phrase overlays=%d, want global budget of five", language, phraseCount)
	}
}

func verifyOverlayReference(t *testing.T, language string, render map[string]any, jobID string, seenFolders map[string]string) {
	t.Helper()
	if render == nil || !isSuccessStatus(stringAt(render, "status")) {
		t.Fatalf("%s render reference is not completed: %s", language, compactJSON(render))
	}
	if stringAt(render, "job_id") == "" {
		t.Fatalf("%s render has no queue job id", language)
	}
	items := mapsAt(render, "items")
	if len(items) != 5 {
		t.Fatalf("%s rendered items=%d, want five", language, len(items))
	}
	for _, item := range items {
		if !isSuccessStatus(stringAt(item, "status")) {
			t.Fatalf("%s item %q is not completed: %s", language, stringAt(item, "item_id"), compactJSON(item))
		}
		artifact := mapAt(item, "artifact")
		if artifact == nil || integerAt(artifact, "frame_count") <= 0 ||
			stringAt(artifact, "drive_link") == "" || stringAt(artifact, "drive_file_id") == "" ||
			stringAt(artifact, "drive_folder_id") == "" {
			t.Fatalf("%s item %q is not a published certified artifact: %s", language, stringAt(item, "item_id"), compactJSON(item))
		}
		folder := stringAt(artifact, "drive_folder_id")
		if other, exists := seenFolders[folder]; exists && other != language {
			t.Fatalf("languages %s and %s share Drive folder %q", other, language, folder)
		}
		seenFolders[folder] = language
	}
}

func verifyElonMuskOverlayDriveTree(t *testing.T, result map[string]any, jobID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	service := newElonMuskDriveService(t, ctx)
	root := getDriveFile(t, ctx, service, elonMuskOverlayDriveRoot)
	if root.Name != "Overlay Chronon" {
		t.Fatalf("Drive root name=%q, want Overlay Chronon", root.Name)
	}
	job := findDriveFolderChild(t, ctx, service, elonMuskOverlayDriveRoot, jobID)
	if job.Name != jobID {
		t.Fatalf("Drive job folder=%q, want returned jobID %q", job.Name, jobID)
	}

	totalFiles := 0
	for _, language := range elonMuskOverlayLanguages {
		languageFolder := findDriveFolderChild(t, ctx, service, job.Id, language)
		overlayFolder := findDriveFolderChild(t, ctx, service, languageFolder.Id, "overlay")
		files := listDriveChildren(t, ctx, service, overlayFolder.Id)
		if len(files) != 5 {
			t.Fatalf("Drive %s/overlay files=%d, want five", language, len(files))
		}

		expected := expectedElonMuskDriveFiles(result, language)
		for _, file := range files {
			if file.MimeType != "video/mp4" {
				t.Fatalf("Drive %s/overlay contains %q with MIME %q, want video/mp4", language, file.Name, file.MimeType)
			}
			if _, ok := expected[file.Id]; !ok {
				t.Fatalf("Drive %s/overlay contains unexpected file %q (%s)", language, file.Name, file.Id)
			}
			delete(expected, file.Id)
			totalFiles++
		}
		if len(expected) != 0 {
			t.Fatalf("Drive %s/overlay is missing %d certified artifact(s): %v", language, len(expected), expected)
		}
	}
	if totalFiles != 50 {
		t.Fatalf("Drive phrase MP4 files=%d, want 50", totalFiles)
	}
	t.Logf("REAL Drive verified: Overlay Chronon/%s/<language>/overlay contains 50 phrase MP4 artifacts", jobID)
}

func expectedElonMuskDriveFiles(result map[string]any, language string) map[string]struct{} {
	render := mapAt(result, "overlay_render")
	if language != "en" {
		render = mapAt(mapAt(result, "localized_overlay_renders"), language)
	}
	expected := make(map[string]struct{}, 5)
	for _, item := range mapsAt(render, "items") {
		if artifact := mapAt(item, "artifact"); artifact != nil {
			if id := stringAt(artifact, "drive_file_id"); id != "" {
				expected[id] = struct{}{}
			}
		}
	}
	return expected
}

func newElonMuskDriveService(t *testing.T, ctx context.Context) *gdrive.Service {
	t.Helper()
	loadDotEnvMissing(t, getenv("PIPELINEGEN_E2E_DOTENV", filepath.Join("..", "..", ".env")))
	configPath := getenv("PIPELINEGEN_E2E_CONFIG", filepath.Join("..", "..", "config.yaml"))
	resolved, err := config.GetResolvedFromPath(configPath)
	if err != nil {
		t.Fatalf("load Drive config %s: %v", configPath, err)
	}
	cfg := resolved.View()
	if cfg == nil {
		t.Fatal("Drive config view is nil")
	}
	if got := cfg.Drive.OverlayRenderFolder(); got != elonMuskOverlayDriveRoot {
		t.Fatalf("configured overlay root=%q, want %q", got, elonMuskOverlayDriveRoot)
	}
	base := filepath.Dir(configPath)
	cfg.Paths.CredentialsFile = anchorToConfig(base, cfg.Paths.CredentialsFile)
	cfg.Paths.TokenFile = anchorToConfig(base, cfg.Paths.TokenFile)
	service, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		t.Fatalf("create real Drive service: %v", err)
	}
	return service
}

func getDriveFile(t *testing.T, ctx context.Context, service *gdrive.Service, id string) *gdrive.File {
	t.Helper()
	file, err := service.Files.Get(id).Fields("id,name,mimeType,parents,trashed").Context(ctx).Do()
	if err != nil {
		t.Fatalf("read Drive file %s: %v", id, err)
	}
	if file.Trashed {
		t.Fatalf("Drive file %s is trashed", id)
	}
	return file
}

func listDriveChildren(t *testing.T, ctx context.Context, service *gdrive.Service, parentID string) []*gdrive.File {
	t.Helper()
	var files []*gdrive.File
	pageToken := ""
	for {
		call := service.Files.List().Q(fmt.Sprintf("'%s' in parents and trashed = false", parentID)).
			Fields("nextPageToken,files(id,name,mimeType,parents,trashed)").
			PageSize(1000).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			t.Fatalf("list Drive children of %s: %v", parentID, err)
		}
		files = append(files, page.Files...)
		pageToken = page.NextPageToken
		if pageToken == "" {
			return files
		}
	}
}

func findDriveFolderChild(t *testing.T, ctx context.Context, service *gdrive.Service, parentID, name string) *gdrive.File {
	t.Helper()
	for _, file := range listDriveChildren(t, ctx, service, parentID) {
		if file.Name == name && file.MimeType == "application/vnd.google-apps.folder" {
			return file
		}
	}
	t.Fatalf("Drive folder %q is missing below %s", name, parentID)
	return nil
}
