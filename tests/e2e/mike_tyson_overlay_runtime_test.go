package e2e

// This is the runtime gate for the Mike Tyson overlay matrix. It deliberately
// talks to the live PipelineGen HTTP API with net/http: no shell, curl, local
// result substitution, or fake renderer is involved.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

type mikeTysonMatrixCase struct {
	id            string
	fixture       string
	people        []string
	phrases       []string
	driveFolderID string
	subfolder     string
	docLanguages  []string
}

type mikeTysonMatrixContract struct {
	SchemaVersion string `json:"schema_version"`
	Cases         []struct {
		ID         string   `json:"id"`
		Fixture    string   `json:"fixture"`
		ResultFile string   `json:"result_file"`
		Persons    []string `json:"persons"`
		Phrases    []string `json:"phrases"`
	} `json:"cases"`
}

type mikeTysonRequestFixture struct {
	Items []struct {
		MediaPlan struct {
			Extraction struct {
				MaxEntitiesPerSegment         int      `json:"max_entities_per_segment"`
				MaxImportantPhrasesPerSegment int      `json:"max_important_phrases_per_segment"`
				ImportantPhrases              []string `json:"important_phrases"`
			} `json:"extraction"`
		} `json:"media_plan"`
		Style  string `json:"style"`
		Source struct {
			SourceText string `json:"source_text"`
		} `json:"source"`
		ScriptParams struct {
			Segments []struct {
				SourceText string `json:"source_text"`
			} `json:"segments"`
		} `json:"script_params"`
		Output struct {
			Render struct {
				Enabled            bool   `json:"enabled"`
				DriveFolderID      string `json:"drive_folder_id"`
				DriveSubfolderName string `json:"drive_subfolder_name"`
			} `json:"render"`
		} `json:"output"`
		Docs struct {
			Languages []string `json:"languages"`
		} `json:"docs"`
	} `json:"items"`
}

type certifiedOverlayItem struct {
	JobID     string
	SHA256    string
	DriveLink string
}

func TestMikeTysonOverlaySemanticGate(t *testing.T) {
	wantPhrases := []string{"Potenza e disciplina"}
	valid := []map[string]any{
		{"kind": "entity_image"},
		{"kind": "text_phrase", "text": "Potenza e disciplina"},
	}
	images, phrases, texts, err := validateMikeTysonOverlayItems(valid, 1, wantPhrases)
	if err != nil || images != 1 || phrases != 1 || !slices.Equal(texts, wantPhrases) {
		t.Fatalf("valid semantic plan rejected: images=%d phrases=%d texts=%v err=%v", images, phrases, texts, err)
	}

	for _, test := range []struct {
		name  string
		items []map[string]any
		want  string
	}{
		{"unknown kind", []map[string]any{{"kind": "entity_image"}, {"kind": "text_phrase", "text": wantPhrases[0]}, {"kind": "mystery"}}, "unsupported/unclassified"},
		{"extra image", []map[string]any{{"kind": "entity_image"}, {"kind": "image"}, {"kind": "text_phrase", "text": wantPhrases[0]}}, "image overlay count=2"},
		{"wrong phrase", []map[string]any{{"kind": "entity_image"}, {"kind": "text_phrase", "text": "wrong"}}, "rendered phrase overlay text"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := validateMikeTysonOverlayItems(test.items, 1, wantPhrases); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestMikeTysonMatrixContract(t *testing.T) {
	cases := mikeTysonMatrixCases(t)
	if len(cases) != 3 {
		t.Fatalf("matrix cases=%d, want 3", len(cases))
	}
	for index, count := range []int{1, 3, 5} {
		if len(cases[index].people) != count || len(cases[index].phrases) != count {
			t.Errorf("case %q has people/phrases=%d/%d, want %d/%d", cases[index].id, len(cases[index].people), len(cases[index].phrases), count, count)
		}
	}
}

func TestLiveMikeTysonOverlayRuntime(t *testing.T) {
	if os.Getenv("PIPELINEGEN_MIKE_TYSON_LIVE") != "1" {
		t.Skip("set PIPELINEGEN_MIKE_TYSON_LIVE=1 to run the live Mike Tyson runtime test")
	}
	token := liveAdminToken()
	if token == "" {
		t.Skip("VELOX_ADMIN_TOKEN or TOKEN_FILE is required for the live Mike Tyson runtime test")
	}

	baseURL := strings.TrimRight(getenv("VELOX_API_BASE_URL", "http://127.0.0.1:8000"), "/")
	client := &http.Client{}
	for _, tc := range mikeTysonMatrixCases(t) {
		if !t.Run(tc.id, func(t *testing.T) {
			body := readMikeTysonFixture(t, tc.fixture)
			jobID := submitMikeTysonRuntime(t, client, baseURL, token, body)
			full := waitForMikeTysonRuntime(t, client, baseURL, token, jobID)
			result := generationResult(full)
			if result == nil {
				t.Fatalf("job %s completed without job.result.result", jobID)
			}
			verifyMikeTysonRuntimeResult(t, result, tc.people, tc.phrases, tc.driveFolderID, tc.subfolder, tc.docLanguages)
			t.Logf("runtime PASS: job=%s people=%d phrases=%d image_renders=%d phrase_renders=%d", jobID, len(tc.people), len(tc.phrases), len(tc.people), len(tc.phrases))
		}) {
			t.Fatalf("stopping Mike Tyson runtime matrix after failed case %q", tc.id)
		}
	}
}

// TestLiveMikeTysonTenLanguageRuntime is the release-sized goal check: a
// persisted 1,000-word/five-scene script must produce source plus nine
// translated overlays and documents from the same run.
func TestLiveMikeTysonTenLanguageRuntime(t *testing.T) {
	if os.Getenv("PIPELINEGEN_MIKE_TYSON_LIVE") != "1" {
		t.Skip("set PIPELINEGEN_MIKE_TYSON_LIVE=1 to run the live 10-language Mike Tyson runtime test")
	}
	token := liveAdminToken()
	if token == "" {
		t.Skip("VELOX_ADMIN_TOKEN or TOKEN_FILE is required for the live Mike Tyson runtime test")
	}
	baseURL := strings.TrimRight(getenv("VELOX_API_BASE_URL", "http://127.0.0.1:8000"), "/")
	client := &http.Client{}
	body := readMikeTysonFixture(t, "ops/jobs/mike_tyson_1000w_5scene_10lang.generate.json")
	jobID := submitMikeTysonRuntime(t, client, baseURL, token, body)
	full := waitForMikeTysonRuntime(t, client, baseURL, token, jobID)
	result := generationResult(full)
	if result == nil {
		t.Fatalf("job %s completed without job.result.result", jobID)
	}
	verifyMikeTysonTenLanguageRuntimeResult(t, result)
	t.Logf("runtime PASS: job=%s script_id=%d languages=10 scenes=5", jobID, integerAt(result, "script_id"))
}

func mikeTysonMatrixCases(t *testing.T) []mikeTysonMatrixCase {
	t.Helper()
	var contract mikeTysonMatrixContract
	if err := json.Unmarshal(readMikeTysonFixture(t, "matrix_cases.json"), &contract); err != nil {
		t.Fatalf("decode Mike Tyson matrix contract: %v", err)
	}
	if contract.SchemaVersion != "mike-tyson-overlay-matrix.v1" {
		t.Fatalf("matrix schema_version=%q, want mike-tyson-overlay-matrix.v1", contract.SchemaVersion)
	}

	wantIDs, wantCounts := []string{"simple", "extended", "five"}, []int{1, 3, 5}
	if len(contract.Cases) != len(wantIDs) {
		t.Fatalf("matrix cases=%d, want %d", len(contract.Cases), len(wantIDs))
	}

	out := make([]mikeTysonMatrixCase, 0, len(contract.Cases))
	for index, entry := range contract.Cases {
		if entry.ID != wantIDs[index] {
			t.Fatalf("matrix case[%d]=%q, want %q", index, entry.ID, wantIDs[index])
		}
		if len(entry.Persons) != wantCounts[index] || len(entry.Phrases) != wantCounts[index] || entry.Fixture == "" || entry.ResultFile == "" || filepath.Base(entry.ResultFile) != entry.ResultFile || strings.Contains(entry.Fixture, "..") || strings.Contains(entry.ResultFile, "..") {
			t.Fatalf("matrix case %q must declare exactly %d persons and phrases plus fixture/result filenames", entry.ID, wantCounts[index])
		}
		if duplicateStrings(entry.Persons) || duplicateStrings(entry.Phrases) {
			t.Fatalf("matrix case %q repeats a person or phrase", entry.ID)
		}
		if filepath.Base(entry.Fixture) != entry.Fixture {
			t.Fatalf("matrix case %q fixture must be a filename", entry.ID)
		}

		var request mikeTysonRequestFixture
		if err := json.Unmarshal(readMikeTysonFixture(t, entry.Fixture), &request); err != nil {
			t.Fatalf("decode matrix fixture %s: %v", entry.Fixture, err)
		}
		if len(request.Items) != 1 {
			t.Fatalf("matrix fixture %s contains %d request items, want 1", entry.Fixture, len(request.Items))
		}
		fixture := request.Items[0]
		requestText := fixture.Style + " " + fixture.Source.SourceText
		for _, segment := range fixture.ScriptParams.Segments {
			requestText += " " + segment.SourceText
		}
		for _, person := range entry.Persons {
			if !strings.Contains(strings.ToLower(requestText), strings.ToLower(person)) {
				t.Fatalf("matrix %s expected person %q is not named in request instructions/source", entry.ID, person)
			}
		}
		if !slices.Equal(entry.Phrases, fixture.MediaPlan.Extraction.ImportantPhrases) {
			t.Fatalf("matrix %s phrases=%v differ from fixture phrases=%v", entry.ID, entry.Phrases, fixture.MediaPlan.Extraction.ImportantPhrases)
		}
		if fixture.MediaPlan.Extraction.MaxEntitiesPerSegment != len(entry.Persons) ||
			fixture.MediaPlan.Extraction.MaxImportantPhrasesPerSegment != len(entry.Phrases) {
			t.Fatalf("matrix %s fixture budgets do not match expected people/phrases", entry.ID)
		}
		if !fixture.Output.Render.Enabled || fixture.Output.Render.DriveFolderID == "" || fixture.Output.Render.DriveSubfolderName == "" {
			t.Fatalf("matrix fixture %s must enable render and declare Drive destination", entry.Fixture)
		}
		if len(fixture.Docs.Languages) == 0 {
			t.Fatalf("matrix fixture %s must declare document languages", entry.Fixture)
		}
		out = append(out, mikeTysonMatrixCase{
			id: entry.ID, fixture: entry.Fixture, people: entry.Persons, phrases: entry.Phrases,
			driveFolderID: fixture.Output.Render.DriveFolderID, subfolder: fixture.Output.Render.DriveSubfolderName,
			docLanguages: fixture.Docs.Languages,
		})
	}
	return out
}

func duplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

// liveAdminToken resolves the admin bearer token shared by every live gate in
// this package: VELOX_ADMIN_TOKEN wins, otherwise TOKEN_FILE is read as a
// shell-style env file carrying `VELOX_ADMIN_TOKEN=...`.
func liveAdminToken() string {
	if token := strings.TrimSpace(os.Getenv("VELOX_ADMIN_TOKEN")); token != "" {
		return token
	}
	path := strings.TrimSpace(os.Getenv("TOKEN_FILE"))
	if path == "" {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		if strings.HasPrefix(line, "VELOX_ADMIN_TOKEN=") {
			return strings.Trim(strings.TrimPrefix(line, "VELOX_ADMIN_TOKEN="), "\"'")
		}
	}
	return ""
}

func readMikeTysonFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while resolving Mike Tyson fixture")
	}
	var path string
	if filepath.ToSlash(name) == "ops/jobs/mike_tyson_1000w_5scene_10lang.generate.json" {
		path = filepath.Join(filepath.Dir(file), "..", "..", "ops", "jobs", filepath.Base(name))
	} else {
		path = filepath.Join(filepath.Dir(file), "..", "..", "..", "RenderingGen", "mike_tyson_overlay_test", name)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if !json.Valid(body) {
		t.Fatalf("fixture %s is not valid JSON", path)
	}
	return body
}

func verifyMikeTysonTenLanguageRuntimeResult(t *testing.T, result map[string]any) {
	t.Helper()
	wantCities := []string{"Brooklyn", "Catskill", "Atlantic City", "Tokyo", "Las Vegas"}
	wantLanguages := []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}
	if integerAt(result, "script_id") <= 0 {
		t.Fatalf("script was not persisted to the script database: %s", compactJSON(result))
	}
	if count := integerAt(mapAt(result, "output"), "word_count"); count <= 0 {
		t.Fatalf("word_count=%d, want non-empty generated narration", count)
	}
	if scenes := mapsAt(result, "scenes"); len(scenes) != 5 {
		t.Fatalf("scenes=%d, want five", len(scenes))
	}
	places := stringValues(valueAt(mapAt(result, "entities"), "places"), "value")
	for _, city := range wantCities {
		if !containsString(places, city) {
			t.Fatalf("extracted places=%v, missing %q", places, city)
		}
	}
	sourcePlan := mapAt(result, "overlay_plan")
	verifyMikeTysonLanguageOverlayPlan(t, "en", sourcePlan, wantCities, nil, mapsAt(result, "scenes"))

	localizedPlans := mapAt(result, "localized_overlay_plans")
	localizedRenders := mapAt(result, "localized_overlay_renders")
	if len(localizedPlans) != 9 || len(localizedRenders) != 9 {
		t.Fatalf("localized plans/renders=%d/%d, want nine each", len(localizedPlans), len(localizedRenders))
	}
	for _, language := range wantLanguages {
		if language == "en" {
			continue
		}
		plan := mapAt(localizedPlans, language)
		verifyMikeTysonLanguageOverlayPlan(t, language, plan, wantCities, nil, mapsAt(result, "scenes"))
		verifyCertifiedOverlayReference(t, language, mapAt(localizedRenders, language), overlayPlanItemIDs(plan))
	}
	verifyCertifiedOverlayReference(t, "en", mapAt(result, "overlay_render"), overlayPlanItemIDs(sourcePlan))

	documents := mapAt(result, "documents")
	if len(documents) != 10 {
		t.Fatalf("localized documents=%d, want ten", len(documents))
	}
	for _, language := range wantLanguages {
		if stringAt(mapAt(documents, language), "link") == "" {
			t.Fatalf("%s document link is missing", language)
		}
	}
}

func verifyMikeTysonLanguageOverlayPlan(t *testing.T, language string, plan map[string]any, cities, phrases []string, scenes []map[string]any) {
	t.Helper()
	if plan == nil {
		t.Fatalf("%s localized overlay plan is missing", language)
	}
	if got := stringAt(plan, "language"); got != language {
		t.Fatalf("overlay plan language=%q, want %q", got, language)
	}
	items := mapsAt(plan, "items")
	phraseTexts := make([]string, 0, 5)
	itemTexts := make([]string, 0, len(items))
	for _, item := range items {
		if text := stringAt(item, "text"); text != "" {
			itemTexts = append(itemTexts, text)
		}
		if stringAt(item, "kind") == "text_phrase" {
			phraseTexts = append(phraseTexts, stringAt(item, "text"))
		}
	}
	if len(phraseTexts) == 0 {
		t.Fatalf("%s has zero phrase overlays; plan=%s", language, compactJSON(plan))
	}
	for _, city := range cities {
		if !containsString(itemTexts, city) {
			t.Fatalf("%s overlay plan does not contain city card %q", language, city)
		}
	}
	for _, phrase := range phrases {
		if !containsString(phraseTexts, phrase) {
			t.Fatalf("%s phrase overlays=%v, missing %q", language, phraseTexts, phrase)
		}
	}
	if scenes != nil {
		verifyMikeTysonLocalizedPhraseBindings(t, language, phraseTexts, items, scenes)
	}
}

// verifyMikeTysonLocalizedPhraseBindings closes the runtime gap between the
// translated annotations and the rendered plan. It checks the exact ordered
// phrase projection, localized text grounding, and the local voiceover window.
// Plan timestamps are global, so scene windows are accumulated from that
// language's voiceover durations.
func verifyMikeTysonLocalizedPhraseBindings(t *testing.T, language string, planPhrases []string, items, scenes []map[string]any) {
	t.Helper()
	wantPhrases := make([]string, 0, len(planPhrases))
	type sceneWindow struct{ startMS, endMS int64 }
	windows := make(map[string]sceneWindow, len(scenes))
	var offsetMS int64
	for index, scene := range scenes {
		sceneID := stringAt(scene, "id")
		if sceneID == "" {
			t.Fatalf("%s scene[%d] has no id", language, index)
		}
		text := stringAt(mapAt(scene, "text"), language)
		if text == "" {
			t.Fatalf("%s scene %q has no localized text", language, sceneID)
		}
		localized := mapAt(mapAt(scene, "annotations_by_language"), language)
		if localized == nil && language == "en" {
			localized = mapAt(scene, "annotations")
		}
		if localized == nil {
			t.Fatalf("%s scene %q has no localized annotations", language, sceneID)
		}
		phrases := stringValues(valueAt(localized, "important_phrases"), "text")
		wantPhrases = append(wantPhrases, phrases...)
		for _, phrase := range phrases {
			if !strings.Contains(text, phrase) {
				t.Fatalf("%s scene %q phrase %q is not contained in localized text %q", language, sceneID, phrase, text)
			}
			words := strings.Fields(phrase)
			if len(words) < 2 || len(words) > 4 {
				t.Fatalf("%s scene %q phrase %q has %d words, want 2..4", language, sceneID, phrase, len(words))
			}
			for _, entity := range append(stringValues(valueAt(localized, "primary_entities"), "text"), stringValues(valueAt(localized, "secondary_entities"), "text")...) {
				if phraseOverlapsSurface(text, phrase, entity) {
					t.Fatalf("%s scene %q phrase %q overlaps entity surface %q", language, sceneID, phrase, entity)
				}
			}
		}
		voiceover := mapAt(mapAt(scene, "voiceover"), language)
		durationMS := int64(math.Ceil(floatAt(voiceover, "duration") * 1000))
		if durationMS <= 0 {
			t.Fatalf("%s scene %q has no positive local voiceover duration: %s", language, sceneID, compactJSON(voiceover))
		}
		windows[sceneID] = sceneWindow{startMS: offsetMS, endMS: offsetMS + durationMS}
		offsetMS += durationMS
	}
	if len(wantPhrases) != len(planPhrases) {
		t.Fatalf("%s localized annotation phrases=%v, plan phrases=%v", language, wantPhrases, planPhrases)
	}
	for index := range wantPhrases {
		if wantPhrases[index] != planPhrases[index] {
			t.Fatalf("%s phrase order/content mismatch at %d: annotation=%q plan=%q", language, index, wantPhrases[index], planPhrases[index])
		}
	}
	for _, item := range items {
		if stringAt(item, "kind") != "text_phrase" {
			continue
		}
		sceneID := stringAt(item, "scene_id")
		window, ok := windows[sceneID]
		if !ok {
			t.Fatalf("%s phrase item %q has unknown scene_id %q", language, stringAt(item, "id"), sceneID)
		}
		startMS, endMS := integerAt(item, "start_ms"), integerAt(item, "end_ms")
		if startMS < window.startMS || endMS > window.endMS {
			t.Fatalf("%s phrase %q timing [%d,%d] escapes local voiceover window [%d,%d] for scene %q", language, stringAt(item, "text"), startMS, endMS, window.startMS, window.endMS, sceneID)
		}
	}
}

func phraseOverlapsSurface(text, phrase, surface string) bool {
	phraseStart := strings.Index(text, phrase)
	entityStart := strings.Index(text, surface)
	if phraseStart < 0 || entityStart < 0 {
		return false
	}
	phraseEnd := phraseStart + len(phrase)
	entityEnd := entityStart + len(surface)
	return phraseStart < entityEnd && entityStart < phraseEnd
}

func verifyCertifiedOverlayReference(t *testing.T, language string, render map[string]any, expectedItemIDs []string) map[string]certifiedOverlayItem {
	t.Helper()
	artifact := mapAt(render, "artifact")
	if !isSuccessStatus(firstNonEmpty(stringAt(render, "status"), stringAt(artifact, "status"))) ||
		!isSHA256Hex(stringAt(artifact, "sha256")) || integerAt(artifact, "size_bytes") <= 0 ||
		integerAt(artifact, "frame_count") <= 0 ||
		firstNonEmpty(stringAt(artifact, "drive_link"), stringAt(artifact, "url")) == "" {
		t.Fatalf("%s overlay artifact is not certified/published: %s", language, compactJSON(render))
	}
	items := mapsAt(render, "items")
	if len(items) != len(expectedItemIDs) {
		t.Fatalf("%s rendered item refs=%d, want %d for the semantic plan", language, len(items), len(expectedItemIDs))
	}
	seen := make(map[string]struct{}, len(items))
	seenJobs := make(map[string]struct{}, len(items))
	certified := make(map[string]certifiedOverlayItem, len(items))
	for index, item := range items {
		itemID, jobID := stringAt(item, "item_id"), stringAt(item, "job_id")
		if index >= len(expectedItemIDs) || itemID != expectedItemIDs[index] {
			t.Fatalf("%s render item[%d]=%q, want plan item %q", language, index, itemID, expectedItemIDs[index])
		}
		if _, duplicate := seen[itemID]; duplicate {
			t.Fatalf("%s render repeats item_id %q", language, itemID)
		}
		itemArtifact := mapAt(item, "artifact")
		itemSHA := stringAt(itemArtifact, "sha256")
		itemDriveLink := firstNonEmpty(stringAt(itemArtifact, "drive_link"), stringAt(itemArtifact, "url"))
		if jobID == "" || !isSuccessStatus(stringAt(item, "status")) || !isSHA256Hex(itemSHA) ||
			integerAt(itemArtifact, "size_bytes") <= 0 || integerAt(itemArtifact, "frame_count") <= 0 || itemDriveLink == "" {
			t.Fatalf("%s per-item render is not certified/published: %s", language, compactJSON(item))
		}
		if _, duplicate := seenJobs[jobID]; duplicate {
			t.Fatalf("%s item render job %q is reused", language, jobID)
		}
		seen[itemID], seenJobs[jobID] = struct{}{}, struct{}{}
		certified[itemID] = certifiedOverlayItem{JobID: jobID, SHA256: itemSHA, DriveLink: stringAt(itemArtifact, "drive_link")}
	}
	if len(expectedItemIDs) == 0 {
		t.Fatalf("%s semantic overlay plan has no renderable items", language)
	}
	if firstNonEmpty(stringAt(render, "job_id")) != certified[expectedItemIDs[0]].JobID ||
		stringAt(artifact, "sha256") != certified[expectedItemIDs[0]].SHA256 {
		t.Fatalf("%s top-level render reference is not the first certified item", language)
	}
	return certified
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func overlayPlanItemIDs(plan map[string]any) []string {
	items := mapsAt(plan, "items")
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := stringAt(item, "id"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func submitMikeTysonRuntime(t *testing.T, client *http.Client, baseURL, token string, body []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/script/generate", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create generate request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("mike-tyson-runtime-%d", time.Now().UnixNano()))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/script/generate: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatalf("read generate response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST /api/script/generate returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var payload map[string]any
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		t.Fatalf("decode generate response: %v; body=%s", err, responseBody)
	}
	jobID := stringAt(payload, "job_id")
	if jobID == "" {
		t.Fatalf("generate response has no job_id: %s", responseBody)
	}
	return jobID
}

func waitForMikeTysonRuntime(t *testing.T, client *http.Client, baseURL, token, jobID string) map[string]any {
	t.Helper()
	timeout := 15 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("MIKE_TYSON_RUNTIME_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		full, status, err := getLiveJob(t, ctx, client, baseURL, token, jobID)
		if err != nil {
			t.Fatalf("GET /api/jobs/%s/full: %v", jobID, err)
		}
		if isSuccessStatus(status) {
			return full
		}
		if isFailureStatus(status) {
			t.Fatalf("runtime job %s failed with status %q: %s", jobID, status, compactJSON(full))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runtime job %s did not complete within %s; last status=%q", jobID, timeout, status)
		case <-ticker.C:
		}
	}
}

// getLiveJob reads the full job view and derives its current status. It is
// shared by the live runtime gates in this package.
func getLiveJob(t *testing.T, ctx context.Context, client *http.Client, baseURL, token, jobID string) (map[string]any, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/jobs/"+jobID+"/full", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var resp *http.Response
	for attempt := 0; attempt < 10; attempt++ {
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if attempt < 9 && ctx.Err() == nil {
			time.Sleep(1 * time.Second)
			// Recreate request body if any, though GET has no body
			req, _ = http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/jobs/"+jobID+"/full", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			continue
		}
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var full map[string]any
	if err := json.Unmarshal(body, &full); err != nil {
		return nil, "", err
	}
	status := strings.ToUpper(firstNonEmpty(
		stringAt(mapAt(full, "job"), "status"),
		stringAt(full, "status"),
		stringAt(full, "current_step"),
		stringAt(full, "current_stage"),
	))
	return full, status, nil
}

func verifyMikeTysonRuntimeResult(t *testing.T, result map[string]any, wantPeople, wantPhrases []string, wantDriveFolderID, wantSubfolder string, wantDocLanguages []string) {
	t.Helper()
	gotPeople := stringValues(valueAt(mapAt(result, "entities"), "persons"), "value")
	assertExactStrings(t, "persons", gotPeople, wantPeople)
	gotPhrases := stringValues(valueAt(mapAt(result, "entities"), "important_phrases"), "")
	assertExactStrings(t, "important_phrases", gotPhrases, wantPhrases)

	plan := mapAt(result, "overlay_plan")
	if plan == nil {
		t.Fatal("result.overlay_plan is missing")
	}
	items := mapsAt(plan, "items")
	_, _, _, err := validateMikeTysonOverlayItems(items, len(wantPeople), wantPhrases)
	if err != nil {
		t.Fatal(err)
	}
	phraseMotions := make(map[string]struct{})
	planIDs := make(map[string]struct{}, len(items))
	planItemsByID := make(map[string]map[string]any, len(items))
	planFingerprint := stringAt(plan, "fingerprint")
	videoID := stringAt(plan, "video_id")
	if planFingerprint == "" || videoID == "" {
		t.Fatal("overlay plan is missing its fingerprint or source video identity")
	}
	for _, item := range items {
		itemID := stringAt(item, "id")
		if itemID == "" {
			t.Fatalf("overlay plan item has no id: %s", compactJSON(item))
		}
		if stringAt(item, "render_key") == "" {
			t.Fatalf("overlay plan item %q is missing its content render_key", itemID)
		}
		if _, duplicate := planIDs[itemID]; duplicate {
			t.Fatalf("overlay plan repeats item id %q", itemID)
		}
		planIDs[itemID] = struct{}{}
		planItemsByID[itemID] = item
		kind := stringAt(item, "kind")
		if kind == "text_phrase" {
			motion := firstNonEmpty(stringAt(item, "motion_id"), animationParam(item))
			if motion == "" {
				t.Fatalf("phrase %q has no runtime motion_id/animation", stringAt(item, "text"))
			}
			phraseMotions[motion] = struct{}{}
		}
		start, end := integerAt(item, "start_ms"), integerAt(item, "end_ms")
		if start < 0 || end <= start {
			t.Fatalf("overlay %q has invalid timing [%d,%d]ms", itemID, start, end)
		}
	}
	if len(phraseMotions) != len(wantPhrases) {
		t.Fatalf("phrase motion count=%d, want %d distinct motions", len(phraseMotions), len(wantPhrases))
	}

	// Image-only plans intentionally omit display text. Join each card to its
	// certified PERSON occurrence instead of accepting a similarly named plan
	// label or an unrelated image card.
	peopleByEntityID := make(map[string]string)
	for _, scene := range mapsAt(mapAt(result, "entity_timeline"), "scenes") {
		for _, entity := range mapsAt(scene, "entities") {
			if stringAt(entity, "type") != "PERSON" {
				continue
			}
			entityID, name := stringAt(entity, "entity_id"), stringAt(entity, "name")
			if entityID != "" && containsString(wantPeople, name) {
				peopleByEntityID[entityID] = name
			}
		}
	}
	seenImagePeople := make(map[string]struct{}, len(wantPeople))
	for _, item := range items {
		if stringAt(item, "kind") != "entity_image" && stringAt(item, "kind") != "image" {
			continue
		}
		entityID := stringAt(item, "entity_id")
		name, ok := peopleByEntityID[entityID]
		if !ok {
			t.Fatalf("image overlay %q is not bound to an expected PERSON occurrence: entity_id=%q", stringAt(item, "id"), entityID)
		}
		if _, duplicate := seenImagePeople[name]; duplicate {
			t.Fatalf("person %q has more than one image overlay", name)
		}
		seenImagePeople[name] = struct{}{}
	}
	if len(seenImagePeople) != len(wantPeople) {
		t.Fatalf("image overlays bound to %v, want exactly %v", sortedKeys(seenImagePeople), wantPeople)
	}
	assertExactStrings(t, "image overlay people", sortedKeys(seenImagePeople), wantPeople)

	overlays := mapsAt(mapAt(result, "editing_timeline"), "overlays")
	if len(overlays) != len(items) {
		t.Fatalf("editing_timeline overlay renders=%d, want one per plan item (%d)", len(overlays), len(items))
	}
	perItemArtifacts := verifyCertifiedOverlayReference(t, "it", mapAt(result, "overlay_render"), overlayPlanItemIDs(plan))
	seenTimelineItems := make(map[string]struct{}, len(overlays))
	seenRenderJobs := make(map[string]struct{})
	for _, overlay := range overlays {
		artifactID := stringAt(overlay, "artifact_id")
		if _, ok := planIDs[artifactID]; !ok {
			t.Fatalf("editing_timeline has unexpected overlay artifact_id %q", artifactID)
		}
		if _, duplicate := seenTimelineItems[artifactID]; duplicate {
			t.Fatalf("editing_timeline repeats artifact_id %q", artifactID)
		}
		seenTimelineItems[artifactID] = struct{}{}
		renderJobID, driveLink, artifactSHA := stringAt(overlay, "render_job_id"), stringAt(overlay, "drive_link"), stringAt(overlay, "sha256")
		if renderJobID == "" || driveLink == "" || !isSHA256Hex(artifactSHA) {
			t.Fatalf("overlay render is missing certified publication lineage: %s", compactJSON(overlay))
		}
		itemRender, ok := perItemArtifacts[artifactID]
		if !ok || renderJobID != itemRender.JobID || artifactSHA != itemRender.SHA256 {
			t.Fatalf("editing_timeline artifact does not match per-item render reference: %s", compactJSON(overlay))
		}
		if itemRender.DriveLink != "" && driveLink != itemRender.DriveLink {
			t.Fatalf("editing_timeline Drive link does not match per-item artifact for %q", artifactID)
		}
		planItem := planItemsByID[artifactID]
		if stringAt(overlay, "plan_fingerprint") != planFingerprint ||
			stringAt(overlay, "render_key") != stringAt(planItem, "render_key") ||
			stringAt(overlay, "source_video_asset_id") != videoID {
			t.Fatalf("editing_timeline provenance differs from frozen plan for %q: %s", artifactID, compactJSON(overlay))
		}
		seenRenderJobs[renderJobID] = struct{}{}
		if integerAt(overlay, "start_us") < 0 || integerAt(overlay, "end_us") <= integerAt(overlay, "start_us") {
			t.Fatalf("overlay render has invalid timing: %s", compactJSON(overlay))
		}
	}
	if len(seenTimelineItems) != len(planIDs) {
		t.Fatalf("editing_timeline covers %d/%d plan items", len(seenTimelineItems), len(planIDs))
	}
	if len(seenRenderJobs) != len(overlays) {
		t.Fatalf("overlay renders reuse render_job_id: %d unique for %d overlays", len(seenRenderJobs), len(overlays))
	}

	documents := mapAt(result, "documents")
	if len(documents) != len(wantDocLanguages) {
		t.Fatalf("published documents=%d, want exactly %d languages", len(documents), len(wantDocLanguages))
	}
	for _, language := range wantDocLanguages {
		if stringAt(mapAt(documents, language), "link") == "" {
			t.Fatalf("%s Google Doc link is missing", language)
		}
	}
	if len(wantDocLanguages) > 1 && integerAt(mapAt(result, "translation_metrics"), "calls") < int64(len(wantDocLanguages)-1) {
		t.Fatalf("translation metrics=%s, want at least %d target call(s)", compactJSON(mapAt(result, "translation_metrics")), len(wantDocLanguages)-1)
	}
	renderConfig := mapAt(result, "render")
	if stringAt(renderConfig, "drive_folder_id") != wantDriveFolderID || stringAt(renderConfig, "drive_subfolder_name") != wantSubfolder {
		t.Fatalf("render Drive routing=%s, want root=%s subfolder=%s", compactJSON(renderConfig), wantDriveFolderID, wantSubfolder)
	}
}

// validateMikeTysonOverlayItems is the side-effect-free semantic gate shared
// by the live runtime assertion and offline contract tests.
func validateMikeTysonOverlayItems(items []map[string]any, wantImages int, wantPhrases []string) (int, int, []string, error) {
	imageCount, phraseCount := 0, 0
	phraseTexts := make([]string, 0, len(wantPhrases))
	for index, item := range items {
		kind := stringAt(item, "kind")
		switch kind {
		case "entity_image", "image":
			imageCount++
		case "text_phrase":
			phraseCount++
			phraseTexts = append(phraseTexts, stringAt(item, "text"))
		default:
			return 0, 0, nil, fmt.Errorf("overlay plan item[%d] has unsupported/unclassified kind %q", index, kind)
		}
	}
	if imageCount != wantImages {
		return 0, 0, nil, fmt.Errorf("image overlay count=%d, want exactly %d", imageCount, wantImages)
	}
	if phraseCount != len(wantPhrases) {
		return 0, 0, nil, fmt.Errorf("phrase overlay count=%d, want exactly %d", phraseCount, len(wantPhrases))
	}
	got := append([]string(nil), phraseTexts...)
	want := append([]string(nil), wantPhrases...)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		return 0, 0, nil, fmt.Errorf("rendered phrase overlay text=%v, want exactly %v", got, want)
	}
	return imageCount, phraseCount, phraseTexts, nil
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func animationParam(item map[string]any) string {
	animation := mapAt(mapAt(item, "params"), "animation")
	return firstNonEmpty(stringAt(animation, "motion_id"), stringAt(animation, "preset"))
}

func generationResult(full map[string]any) map[string]any {
	if result := mapAt(mapAt(full, "job"), "result"); result != nil {
		if nested := mapAt(result, "result"); nested != nil {
			return nested
		}
		return result
	}
	if result := mapAt(full, "result"); result != nil {
		if nested := mapAt(result, "result"); nested != nil {
			return nested
		}
		return result
	}
	return nil
}

func mapAt(value any, key string) map[string]any {
	if object, ok := value.(map[string]any); ok && object != nil {
		if child, ok := object[key].(map[string]any); ok {
			return child
		}
	}
	return nil
}

func valueAt(object map[string]any, key string) any {
	if object == nil {
		return nil
	}
	return object[key]
}

func mapsAt(value any, key string) []map[string]any {
	if object, ok := value.(map[string]any); ok {
		if raw, ok := object[key].([]any); ok {
			out := make([]map[string]any, 0, len(raw))
			for _, item := range raw {
				if child, ok := item.(map[string]any); ok {
					out = append(out, child)
				}
			}
			return out
		}
	}
	return nil
}

func stringValues(value any, field string) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if field == "" {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
			continue
		}
		if object, ok := item.(map[string]any); ok {
			if text := stringAt(object, field); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func assertExactStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s=%v, want exactly %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s=%v, want exactly %v", label, got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringAt(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	value, _ := object[key].(string)
	return strings.TrimSpace(value)
}

func integerAt(object map[string]any, key string) int64 {
	if object == nil {
		return 0
	}
	value, ok := object[key].(float64)
	if !ok {
		return 0
	}
	return int64(value)
}

func floatAt(object map[string]any, key string) float64 {
	if object == nil {
		return 0
	}
	value, _ := object[key].(float64)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isSuccessStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETED", "READY", "SUCCEEDED", "SUCCESS", "UPLOADED":
		return true
	default:
		return false
	}
}

func isFailureStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FAILED", "ERROR", "CANCELLED", "CANCELED", "REJECTED":
		return true
	default:
		return false
	}
}

func compactJSON(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
