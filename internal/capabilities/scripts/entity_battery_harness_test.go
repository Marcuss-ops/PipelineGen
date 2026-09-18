// Package scriptgeneration — entity_battery_harness_test.go drives each corpus
// script through the REAL production chain and measures it.
//
// Only the external participants are stubbed, exactly like the existing
// vertical-slice certifications: the text generator (LLM), the voiceover
// generator (TTS + word timing), the combined audio renderer (Rust master mix)
// and the RenderingGen queue. The whole semantic chain under test is
// production code:
//
//	GenerationEnvelopeV2 → BuildGenerateRequest
//	  → runner scene generation            (script)
//	  → VidRush segment entities           (extraction seam)
//	  → projectEntityAnnotations           (grounding + type normalization)
//	  → applySegmentEntityResults          (typed per-scene aggregate)
//	  → compileResultEntityTimeline        (timestamp / scene association)
//	  → compileResultOverlayPlan           (entity cards + asset promotion)
package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// batteryBarrier is the extraction seam: it returns the fenced per-scene
// segment results the corpus declares as raw NER output.
type batteryBarrier struct {
	segments []scriptpkg.VidRushSegmentResult
}

func (b batteryBarrier) WaitForVidRush(context.Context, string) ([]scriptpkg.VidRushSegmentResult, error) {
	return b.segments, nil
}

// batteryRenderEnqueuer stands in for the RenderingGen queue: the runner hands
// it the frozen semantic overlay plan and receives a certified artifact back,
// exactly like the production queue boundary.
type batteryRenderEnqueuer struct {
	plans []capabilityoverlay.OverlayPlan
}

func (e *batteryRenderEnqueuer) EnqueueChrononPlan(_ context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.plans = append(e.plans, plan)
	return RenderReference{
		JobID:  plan.PlanID,
		Status: "COMPLETED",
		Artifact: &RenderArtifact{
			ID: "battery-overlay-" + plan.PlanID, Kind: "overlay",
			SHA256: "battery-overlay-sha", MimeType: "video/quicktime", SizeBytes: 1024,
			Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Codec: "prores",
			DriveFileID: "battery-drive-" + plan.PlanID,
			DriveLink:   "https://drive.google.com/file/d/battery-drive-" + plan.PlanID + "/view",
		},
	}, nil
}

// batteryDetection is one entity occurrence on the certified EntityTimeline —
// the post-grounding, post-normalization, post-timestamping surface the
// overlay resolver reads.
type batteryDetection struct {
	Scene    string
	EntityID string
	Name     string
	Type     string
	StartUS  int64
	EndUS    int64
}

func batteryDetections(result *GenerateResult) []batteryDetection {
	if result == nil || result.EntityTimeline == nil {
		return nil
	}
	var out []batteryDetection
	for _, scene := range result.EntityTimeline.Scenes {
		for _, occ := range scene.Entities {
			out = append(out, batteryDetection{
				Scene: scene.SceneID, EntityID: occ.EntityID, Name: occ.Name,
				Type: occ.Type, StartUS: occ.AudioStartUS, EndUS: occ.AudioEndUS,
			})
		}
	}
	return out
}

// runEntityBatteryScript executes one corpus script through the production
// runner and returns its completed result.
func runEntityBatteryScript(t *testing.T, s batteryScript) *GenerateResult {
	t.Helper()
	scenes := make([]Scene, len(s.Scenes))
	segments := make([]scriptpkg.VidRushSegmentResult, len(s.Scenes))
	for i, sc := range s.Scenes {
		scenes[i] = Scene{
			ID: sc.ID, Index: i,
			Text:         map[Language]string{"en": sc.Text},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		}
		entities := make([]scriptpkg.ExtractedEntity, len(sc.Raw))
		for j, raw := range sc.Raw {
			entities[j] = scriptpkg.ExtractedEntity{Value: raw.Value, Type: raw.Type, Confidence: raw.Confidence}
		}
		segments[i] = scriptpkg.VidRushSegmentResult{
			SegmentID: sc.ID, SceneID: sc.ID, Position: i, Text: sc.Text,
			TextHash: SceneTextHash(sc.Text),
			Insights: scriptpkg.SegmentInsights{SegmentID: sc.ID, TextHash: SceneTextHash(sc.Text), Entities: entities},
		}
	}

	repo := newInMemRunRepository()
	runner := NewRunner(repo, newStubTextGenerator(scenes), newStubTranslator(), &entityTimelineVoiceoverGenerator{}, newStubDocumentPublisher(), canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayRenderEnqueuer(&batteryRenderEnqueuer{})
	runner.SetLocalizedRenderEnqueuer(&recordingLocalizedRenderEnqueuer{})
	runner.SetVidRushBarrier(batteryBarrier{segments: segments})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Source.Topic = s.Topic
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "entity-battery-" + s.ID
	req.Render.Enabled = true
	req.ExtractEntities = scriptpkg.ToggleEnabled
	req.Title = s.Topic

	runID := "run-" + s.ID
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 20*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "%s (%s) must complete: %s", s.ID, s.Category, final.ErrorMessage)
	require.NotNil(t, final.Result)
	return final.Result
}

// batteryMetrics is the simple, auditable metric block the goal asks for.
type batteryMetrics struct {
	Expected   int
	Detected   int
	Correct    int
	Missed     int
	False      int
	Duplicates int
}

func (m batteryMetrics) Precision() float64 {
	if m.Detected == 0 {
		return 1
	}
	return float64(m.Correct) / float64(m.Detected)
}

func (m batteryMetrics) Recall() float64 {
	if m.Expected == 0 {
		return 1
	}
	return float64(m.Correct) / float64(m.Expected)
}

func (m batteryMetrics) F1() float64 {
	p, r := m.Precision(), m.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

func (m batteryMetrics) Add(other batteryMetrics) batteryMetrics {
	return batteryMetrics{
		Expected: m.Expected + other.Expected, Detected: m.Detected + other.Detected,
		Correct: m.Correct + other.Correct, Missed: m.Missed + other.Missed,
		False: m.False + other.False, Duplicates: m.Duplicates + other.Duplicates,
	}
}

func batteryExpectedKey(scene, entityType, name string) string {
	return scene + "\x00" + strings.ToUpper(strings.TrimSpace(entityType)) + "\x00" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// batteryScriptOutcome is one script's measured result plus the error cases
// that make a failure identifiable instead of "the JSON looks right".
type batteryScriptOutcome struct {
	Script            batteryScript
	Metrics           batteryMetrics
	Missed            []string
	False             []string
	VariantDuplicates []string
	Detected          []batteryDetection
	Result            *GenerateResult
}

// nameVariant reports whether two surface names denote the same identity under
// a token-subset relation (the "Reigns" / "Roman Reigns" shape), so a leaked
// partial mention is counted as a duplicate rather than only a false positive.
func nameVariant(a, b string) bool {
	ta := strings.Fields(strings.ToLower(strings.TrimSpace(a)))
	tb := strings.Fields(strings.ToLower(strings.TrimSpace(b)))
	if len(ta) == 0 || len(tb) == 0 || len(ta) == len(tb) {
		return false
	}
	small, large := ta, tb
	if len(small) > len(large) {
		small, large = large, small
	}
	has := make(map[string]bool, len(large))
	for _, token := range large {
		has[token] = true
	}
	for _, token := range small {
		if !has[token] {
			return false
		}
	}
	return true
}

func measureBatteryScript(s batteryScript, result *GenerateResult) batteryScriptOutcome {
	out := batteryScriptOutcome{Script: s, Result: result, Detected: batteryDetections(result)}

	expected := map[string]batteryExpected{}
	for _, e := range s.Expected {
		expected[batteryExpectedKey(e.Scene, e.Type, e.Name)] = e
	}
	detected := map[string]batteryDetection{}
	for _, d := range out.Detected {
		key := batteryExpectedKey(d.Scene, d.Type, d.Name)
		if _, duplicate := detected[key]; duplicate {
			out.Metrics.Duplicates++
			continue
		}
		detected[key] = d
	}

	out.Metrics.Expected = len(expected)
	out.Metrics.Detected = len(detected)
	for key, e := range expected {
		if _, ok := detected[key]; ok {
			out.Metrics.Correct++
			continue
		}
		out.Metrics.Missed++
		out.Missed = append(out.Missed, fmt.Sprintf("%s/%s@%s", e.Type, e.Name, e.Scene))
	}
	for key, d := range detected {
		if _, ok := expected[key]; ok {
			continue
		}
		out.Metrics.False++
		out.False = append(out.False, fmt.Sprintf("%s/%s@%s", d.Type, d.Name, d.Scene))
		// A surviving partial/surface variant of an expected identity is
		// reported separately: it is the "same entity duplicated with a
		// slightly different name" failure the goal calls out.
		for _, e := range s.Expected {
			if e.Scene == d.Scene && e.Type == d.Type && nameVariant(d.Name, e.Name) {
				out.Metrics.Duplicates++
				out.VariantDuplicates = append(out.VariantDuplicates, fmt.Sprintf("%s vs %s @%s", d.Name, e.Name, d.Scene))
				break
			}
		}
	}
	sort.Strings(out.Missed)
	sort.Strings(out.False)
	sort.Strings(out.VariantDuplicates)
	return out
}

func aggregateBatteryMetrics(outcomes []batteryScriptOutcome) batteryMetrics {
	var total batteryMetrics
	for _, o := range outcomes {
		total = total.Add(o.Metrics)
	}
	return total
}

// batteryEnvelopeJSON renders the wire envelope the generate endpoint accepts
// for one corpus script (topic only, entity extraction explicitly enabled).
func batteryEnvelopeJSON(s batteryScript) string {
	env := map[string]any{
		"version": 2,
		"items": []any{map[string]any{
			"title":    s.Topic,
			"language": "en",
			"source":   map[string]any{"type": "text", "topic": s.Topic},
			"output":   map[string]any{"extract_entities": true},
		}},
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// buildBatteryRequest runs the real generate-endpoint builder for one script.
func buildBatteryRequest(t *testing.T, s batteryScript) GenerateRequest {
	t.Helper()
	var env scriptpkg.GenerationEnvelopeV2
	require.NoError(t, json.Unmarshal([]byte(batteryEnvelopeJSON(s)), &env), "%s envelope must decode", s.ID)
	req, err := BuildGenerateRequest(&env, "battery-key-"+s.ID)
	require.NoError(t, err, "%s generate endpoint must accept the request", s.ID)
	return req
}

func batteryPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value*100)
}

// batteryLevels is the reporting projection of the two certification levels:
// semantic (Generate / Entities) and visual (Timing / Overlay). The
// authoritative gates live in the certification test; this only renders them.
type batteryLevels struct {
	Generate bool
	Entities bool
	Timing   bool
	Overlay  bool
}

func (l batteryLevels) verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func batteryLevelsFor(outcome batteryScriptOutcome) batteryLevels {
	levels := batteryLevels{}
	result := outcome.Result
	if result == nil {
		return levels
	}
	levels.Generate = true
	levels.Entities = outcome.Metrics.Missed == 0 && outcome.Metrics.False == 0 && outcome.Metrics.Duplicates == 0
	if result.EntityTimeline == nil || result.OverlayPlan == nil {
		return levels
	}

	byEntity := map[string][]capabilityoverlay.OverlayItem{}
	entityOverlays := 0
	for _, item := range result.OverlayPlan.Items {
		if item.EntityRef == nil {
			continue
		}
		entityOverlays++
		key := item.SceneID + "\x00" + item.EntityRef.EntityID
		byEntity[key] = append(byEntity[key], item)
	}
	levels.Overlay = entityOverlays == len(outcome.Detected)
	timing := true
	for _, d := range outcome.Detected {
		items := byEntity[d.Scene+"\x00"+d.EntityID]
		if len(items) != 1 {
			levels.Overlay = false
			timing = false
			continue
		}
		item := items[0]
		if item.EntityRef.Name != d.Name || item.EntityRef.Type != d.Type {
			levels.Overlay = false
		}
		if item.StartMs != d.StartUS/1000 {
			timing = false
		}
	}
	levels.Timing = timing
	return levels
}

// renderBatteryReport produces the per-script + global report the goal
// specifies, so a run is auditable from the test output alone.
func renderBatteryReport(outcomes []batteryScriptOutcome) string {
	var b strings.Builder
	b.WriteString("\n=== Goal 4 — entity extraction / overlay battery ===\n")
	for _, o := range outcomes {
		m := o.Metrics
		fmt.Fprintf(&b, "\n%s (%s)\n", o.Script.ID, o.Script.Category)
		fmt.Fprintf(&b, "  Topic:      %s\n", o.Script.Topic)
		fmt.Fprintf(&b, "  Expected:   %d\n  Detected:   %d\n  Correct:    %d\n  Missing:    %d\n  False:      %d\n  Duplicates: %d\n",
			m.Expected, m.Detected, m.Correct, m.Missed, m.False, m.Duplicates)
		fmt.Fprintf(&b, "  Precision:  %s\n  Recall:     %s\n  F1:         %s\n",
			batteryPercent(m.Precision()), batteryPercent(m.Recall()), batteryPercent(m.F1()))
		levels := batteryLevelsFor(o)
		fmt.Fprintf(&b, "  Generate:   %s\n  Entities:   %s\n  Timing:     %s\n  Overlay:    %s\n",
			levels.verdict(levels.Generate), levels.verdict(levels.Entities),
			levels.verdict(levels.Timing), levels.verdict(levels.Overlay))
		if len(o.Missed) > 0 {
			fmt.Fprintf(&b, "  MISSING:    %s\n", strings.Join(o.Missed, ", "))
		}
		if len(o.False) > 0 {
			fmt.Fprintf(&b, "  FALSE:      %s\n", strings.Join(o.False, ", "))
		}
		if len(o.VariantDuplicates) > 0 {
			fmt.Fprintf(&b, "  DUPLICATE:  %s\n", strings.Join(o.VariantDuplicates, ", "))
		}
	}
	total := aggregateBatteryMetrics(outcomes)
	generate, entities, timing, overlay := 0, 0, 0, 0
	for _, o := range outcomes {
		levels := batteryLevelsFor(o)
		if levels.Generate {
			generate++
		}
		if levels.Entities {
			entities++
		}
		if levels.Timing {
			timing++
		}
		if levels.Overlay {
			overlay++
		}
	}
	fmt.Fprintf(&b, "\n=== GLOBAL SUMMARY — %d scripts ===\n", len(outcomes))
	n := len(outcomes)
	fmt.Fprintf(&b, "Generate endpoint        %d/%d\nEntity extraction        %d/%d\nEntity timing            %d/%d\nOverlay generation       %d/%d\n",
		generate, n, entities, n, timing, n, overlay, n)
	fmt.Fprintf(&b, "entities_expected   %d\nentities_detected   %d\ncorrect_entities    %d\nmissed_entities     %d\nfalse_entities      %d\nduplicate_entities  %d\n",
		total.Expected, total.Detected, total.Correct, total.Missed, total.False, total.Duplicates)
	fmt.Fprintf(&b, "NER precision       %s\nNER recall          %s\nNER F1              %s\n",
		batteryPercent(total.Precision()), batteryPercent(total.Recall()), batteryPercent(total.F1()))
	return b.String()
}
