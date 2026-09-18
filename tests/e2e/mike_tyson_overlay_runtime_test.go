package e2e

// This is the runtime gate for the Mike Tyson overlay matrix. It deliberately
// talks to the live PipelineGen HTTP API with net/http: no shell, curl, local
// fixture substitution, or fake renderer is involved.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	mikeTysonDriveRoot       = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	mikeTysonSimpleSubfolder = "Mike Tyson — potenza e disciplina"
	mikeTysonLongSubfolder   = "Mike Tyson — 3 entità 3 frasi"
)

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

	cases := []struct {
		name          string
		fixture       string
		people        []string
		phrases       []string
		images        int
		phraseRenders int
		subfolder     string
		docLanguages  []string
	}{
		{
			name:    "1 persona + 1 frase",
			fixture: "mike_tyson_generate_request.json",
			people:  []string{"Mike Tyson"},
			phrases: []string{"Potenza e disciplina"},
			images:  1, phraseRenders: 1,
			subfolder: mikeTysonSimpleSubfolder,
		},
		{
			name:    "3 entità + 3 frasi",
			fixture: "mike_tyson_extended_generate_request.json",
			people:  []string{"Mike Tyson", "Cus D'Amato", "Muhammad Ali"},
			phrases: []string{
				"La velocità apre la distanza.",
				"La pressione mantiene il controllo.",
				"La disciplina trasforma la potenza.",
			},
			images: 3, phraseRenders: 3,
			subfolder: mikeTysonLongSubfolder,
		},
		{
			name:    "5 entità + 5 frasi",
			fixture: "mike_tyson_five_generate_request.json",
			people:  []string{"Mike Tyson", "Cus D'Amato", "Muhammad Ali", "Sugar Ray Robinson", "Joe Frazier"},
			phrases: []string{
				"La velocità apre la distanza.",
				"La pressione mantiene il controllo.",
				"La disciplina trasforma la potenza.",
				"Il ritmo costruisce il vantaggio.",
				"La tecnica sostiene il coraggio.",
			},
			images: 5, phraseRenders: 5,
			subfolder:    "Mike Tyson — 5 entità 5 frasi",
			docLanguages: []string{"it", "en"},
		},
	}

	for _, tc := range cases {
		if !t.Run(tc.name, func(t *testing.T) {
			body := readMikeTysonFixture(t, tc.fixture)
			jobID := submitMikeTysonRuntime(t, client, baseURL, token, body)
			full := waitForMikeTysonRuntime(t, client, baseURL, token, jobID)
			result := generationResult(full)
			if result == nil {
				t.Fatalf("job %s completed without job.result.result", jobID)
			}
			verifyMikeTysonRuntimeResult(t, result, tc.people, tc.phrases, tc.images, tc.phraseRenders, tc.subfolder, tc.docLanguages)
			t.Logf("runtime PASS: job=%s people=%d phrases=%d image_renders=%d phrase_renders=%d", jobID, len(tc.people), len(tc.phrases), tc.images, tc.phraseRenders)
		}) {
			t.Fatalf("stopping Mike Tyson runtime matrix after failed case %q", tc.name)
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
	wantPhrases := []string{
		"Origins Shape the Fighter.",
		"Discipline Builds the Foundation.",
		"Precision Changes the Outcome.",
		"A Loss Can Rewrite a Record.",
		"A Comeback Starts With One Round.",
	}
	wantLanguages := []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}
	if integerAt(result, "script_id") <= 0 {
		t.Fatalf("script was not persisted to the script database: %s", compactJSON(result))
	}
	if count := integerAt(mapAt(result, "output"), "word_count"); count < 930 {
		t.Fatalf("word_count=%d, want at least 930 within tolerance of the 1,000-word target", count)
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
	assertExactStrings(t, "important_phrases", stringValues(valueAt(mapAt(result, "entities"), "important_phrases"), ""), wantPhrases)

	sourcePlan := mapAt(result, "overlay_plan")
	verifyMikeTysonLanguageOverlayPlan(t, "en", sourcePlan, wantCities, wantPhrases)
	localizedPlans := mapAt(result, "localized_overlay_plans")
	localizedRenders := mapAt(result, "localized_overlay_renders")
	if len(localizedPlans) != 9 || len(localizedRenders) != 9 {
		t.Fatalf("localized plans/renders=%d/%d, want nine each", len(localizedPlans), len(localizedRenders))
	}
	for _, language := range wantLanguages {
		if language == "en" {
			continue
		}
		verifyMikeTysonLanguageOverlayPlan(t, language, mapAt(localizedPlans, language), wantCities, nil)
		verifyCertifiedOverlayReference(t, language, mapAt(localizedRenders, language))
	}
	verifyCertifiedOverlayReference(t, "en", mapAt(result, "overlay_render"))

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

func verifyMikeTysonLanguageOverlayPlan(t *testing.T, language string, plan map[string]any, cities, phrases []string) {
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
	if len(phraseTexts) != 5 {
		t.Fatalf("%s phrase overlays=%d, want five; plan=%s", language, len(phraseTexts), compactJSON(plan))
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
}

func verifyCertifiedOverlayReference(t *testing.T, language string, render map[string]any) {
	t.Helper()
	artifact := mapAt(render, "artifact")
	if !isSuccessStatus(firstNonEmpty(stringAt(render, "status"), stringAt(artifact, "status"))) ||
		integerAt(artifact, "frame_count") <= 0 ||
		firstNonEmpty(stringAt(artifact, "drive_link"), stringAt(artifact, "url")) == "" {
		t.Fatalf("%s overlay artifact is not certified/published: %s", language, compactJSON(render))
	}
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

func verifyMikeTysonRuntimeResult(t *testing.T, result map[string]any, wantPeople, wantPhrases []string, wantImages, wantPhraseRenders int, wantSubfolder string, wantDocLanguages []string) {
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
	imageCount, phraseCount := 0, 0
	phraseMotions := make(map[string]struct{})
	for _, item := range items {
		kind := stringAt(item, "kind")
		if kind == "entity_image" || kind == "image" {
			imageCount++
		}
		if kind == "text_phrase" {
			phraseCount++
			motion := firstNonEmpty(stringAt(item, "motion_id"), animationParam(item))
			if motion == "" {
				t.Fatalf("phrase %q has no runtime motion_id/animation", stringAt(item, "text"))
			}
			phraseMotions[motion] = struct{}{}
		}
		start, end := integerAt(item, "start_ms"), integerAt(item, "end_ms")
		if start < 0 || end <= start {
			t.Fatalf("overlay %q has invalid timing [%d,%d]ms", stringAt(item, "id"), start, end)
		}
	}
	if imageCount != wantImages {
		t.Fatalf("image overlay count=%d, want=%d items=%s", imageCount, wantImages, compactJSON(items))
	}
	if phraseCount != len(wantPhrases) {
		t.Fatalf("phrase overlay count=%d, want=%d", phraseCount, len(wantPhrases))
	}
	if len(phraseMotions) != len(wantPhrases) {
		t.Fatalf("phrase motion count=%d, want %d distinct motions", len(phraseMotions), len(wantPhrases))
	}

	overlays := mapsAt(mapAt(result, "editing_timeline"), "overlays")
	if len(overlays) != wantImages+wantPhraseRenders {
		t.Fatalf("editing_timeline overlay renders=%d, want=%d", len(overlays), wantImages+wantPhraseRenders)
	}
	seenRenderJobs := make(map[string]struct{})
	for _, overlay := range overlays {
		if stringAt(overlay, "artifact_id") == "" || stringAt(overlay, "render_job_id") == "" || stringAt(overlay, "drive_link") == "" {
			t.Fatalf("overlay render is not published: %s", compactJSON(overlay))
		}
		seenRenderJobs[stringAt(overlay, "render_job_id")] = struct{}{}
		if integerAt(overlay, "start_us") < 0 || integerAt(overlay, "end_us") <= integerAt(overlay, "start_us") {
			t.Fatalf("overlay render has invalid timing: %s", compactJSON(overlay))
		}
	}
	if len(seenRenderJobs) != len(overlays) {
		t.Fatalf("overlay renders reuse render_job_id: %d unique for %d overlays", len(seenRenderJobs), len(overlays))
	}

	render := mapAt(result, "overlay_render")
	artifact := mapAt(render, "artifact")
	if !isSuccessStatus(firstNonEmpty(stringAt(render, "status"), stringAt(artifact, "status"))) || integerAt(artifact, "frame_count") <= 0 || firstNonEmpty(stringAt(artifact, "drive_link"), stringAt(artifact, "url")) == "" {
		t.Fatalf("overlay render artifact is not certified: %s", compactJSON(render))
	}

	doc := mapAt(mapAt(result, "documents"), "it")
	if stringAt(doc, "link") == "" {
		t.Fatal("Italian Google Doc link is missing")
	}
	for _, language := range wantDocLanguages {
		if published := mapAt(mapAt(result, "documents"), language); stringAt(published, "link") == "" {
			t.Fatalf("%s Google Doc link is missing", language)
		}
	}
	if len(wantDocLanguages) > 1 && integerAt(mapAt(result, "translation_metrics"), "calls") < int64(len(wantDocLanguages)-1) {
		t.Fatalf("translation metrics=%s, want at least %d target call(s)", compactJSON(mapAt(result, "translation_metrics")), len(wantDocLanguages)-1)
	}
	renderConfig := mapAt(result, "render")
	if stringAt(renderConfig, "drive_folder_id") != mikeTysonDriveRoot || stringAt(renderConfig, "drive_subfolder_name") != wantSubfolder {
		t.Fatalf("render Drive routing=%s, want root=%s subfolder=%s", compactJSON(renderConfig), mikeTysonDriveRoot, wantSubfolder)
	}
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
	case "COMPLETED", "READY", "SUCCEEDED", "SUCCESS":
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
