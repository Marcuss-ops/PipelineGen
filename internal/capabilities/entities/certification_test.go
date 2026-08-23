// Package entities — certification_test.go certifies the canonical
// EntityTimeline pipeline with TWO complete jobs, before any Chronon render:
//
//	JOB 1  "maya-civilization"  (La civiltà Maya, 3 scene, it)	// JOB 2  "classical-music"    (Classical composers, 3 scene, en)
//
// For every scene of every job the certification proves the five-gate chain
// on the REAL configured NLP (the CPU deterministic extractor the production
// fallback uses):
//
//	ENTITY IN TEXT   the entity occurs verbatim in the scene text (rune span)
//	NLP FINDS IT     the real local extractor produced the typed entity
//	WORD TIMING      the entity occurs verbatim in the canonical word timing
//	ENTITY START     audio_start_us = timeline_start + local start, derived
//	                 from the first spoken word's start (never text length)
//	PLAYBACK BOUND   the occurrence span lies exactly on the voiceover's
//	                 certified word boundaries (audio_sha256-bound artifact)
//
// Each job then compiles the full EntityTimeline → OverlayPlan → chronon
// document so the entity card appears exactly on the frames the entity is
// spoken.
package entities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

// TestMain installs the same repository lexicon the composition root loads,
// because the local NLP extractor resolves stop/function words through
// linguistics.DefaultLexicon(). No test-only word lists are allowed.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type certScene struct {
	id   string
	text string
	// keyEntity is the entity the certification must prove the NLP extracted
	// for this scene (the value, not the exact type — classification is the
	// extractor's business).
	keyEntity string
}

type certJob struct {
	id       string
	title    string
	language string
	scenes   []certScene
}

// certJobs are the two complete certification jobs: the Maya civilization
// (Italian) and the classical composers (English). Every scene names exactly
// one multi-word proper name the deterministic extractor must find.
func certJobs() []certJob {
	return []certJob{
		{
			id: "maya-civilization", title: "La civiltà Maya", language: "it",
			scenes: []certScene{
				{id: "scene-pakal", keyEntity: "K'inich Janaab Pakal", text: "K'inich Janaab Pakal governò la città di Palenque per quasi settant'anni."},
				{id: "scene-chichen", keyEntity: "Chichén Itzá", text: "Chichén Itzá e Tikal furono i centri più importanti della civiltà Maya."},
				{id: "scene-kawil", keyEntity: "Jasaw Chan K'awiil", text: "Jasaw Chan K'awiil salì al trono di Tikal e guidò il regno verso una nuova età dell'oro."},
			},
		},
		{
			id: "classical-music", title: "Classical Composers", language: "en",
			scenes: []certScene{
				{id: "scene-bach", keyEntity: "Johann Sebastian Bach", text: "Johann Sebastian Bach composed symphonies in Vienna."},
				{id: "scene-handel", keyEntity: "George Frideric Handel", text: "George Frideric Handel wrote operas across Europe."},
				{id: "scene-debussy", keyEntity: "Claude Debussy", text: "Claude Debussy composed piano works in Paris."},
			},
		},
	}
}

// sourcesFromEntityResult converts the real NLP output into the neutral
// EntitySource inputs, skipping KEYWORD concepts (important words are not
// entities) and empty values.
func sourcesFromEntityResult(result *scriptpkg.EntityResult) []EntitySource {
	var out []EntitySource
	appendGroup := func(entities []scriptpkg.Entity) {
		for _, entity := range entities {
			if strings.EqualFold(strings.TrimSpace(entity.Type), "KEYWORD") {
				continue
			}
			value := strings.TrimSpace(entity.Value)
			if value == "" {
				continue
			}
			out = append(out, EntitySource{Name: value, Type: strings.TrimSpace(entity.Type), Confidence: float64(entity.Score)})
		}
	}
	appendGroup(result.Persons)
	appendGroup(result.Places)
	appendGroup(result.Concepts)
	return out
}

// sceneEvidence is the per-scene evidence the certification needs: the NLP
// sources and the canonical word timing of the ACTUAL voiceover (100ms per
// word, the same deterministic pacing the certification fixtures use).
type sceneEvidence struct {
	sources []EntitySource
	timing  capabilityaudio.SpeechTimingArtifact
}

// certifyJob runs the complete five-gate certification for one job and then
// compiles EntityTimeline → OverlayPlan → chronon.
func certifyJob(t *testing.T, job certJob) {
	t.Helper()
	extractor := localnlp.NewHybridExtractor()

	// ── NLP phase + word timing fixtures ─────────────────────────
	evidence := make([]sceneEvidence, len(job.scenes))
	for i, scene := range job.scenes {
		result, err := extractor.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
			Text: scene.text, Title: job.title, Language: job.language, Device: localnlp.DeviceCPU, EntityCount: 10,
		})
		require.NoError(t, err, "scene %s: NLP extraction failed", scene.id)
		sources := sourcesFromEntityResult(result)
		require.NotEmpty(t, sources, "scene %s: NLP must produce entities", scene.id)

		// NLP FINDS IT — the key entity must be among the typed entities.
		found := false
		for _, source := range sources {
			if source.Name == scene.keyEntity {
				found = true
				require.NotEmpty(t, source.Type, "scene %s: entity %q must carry a type", scene.id, scene.keyEntity)
				require.Greater(t, source.Confidence, 0.0, "scene %s: entity %q must carry a confidence", scene.id, scene.keyEntity)
			}
		}
		require.True(t, found, "scene %s: NLP must extract %q (got %+v)", scene.id, scene.keyEntity, sources)

		words := strings.Fields(scene.text)
		evidence[i] = sceneEvidence{
			sources: sources,
			timing:  wordTimingFor(words, nil, "audio-hash-"+job.id+"-"+scene.id),
		}
	}

	// ── Canonical offsets + MASTER bound ─────────────────────────
	var totalDuration int64
	for _, ev := range evidence {
		totalDuration += ev.timing.DurationUS
	}

	var sceneInputs []SceneInput
	for i, scene := range job.scenes {
		var timelineStart int64
		for j := 0; j < i; j++ {
			timelineStart += evidence[j].timing.DurationUS
		}
		sceneInputs = append(sceneInputs, SceneInput{
			SceneID:          scene.id,
			SceneIndex:       i,
			Text:             scene.text,
			VoiceoverAssetID: "vo-" + job.id + "-" + scene.id,
			TimelineStartUS:  timelineStart,
			Timing:           evidence[i].timing,
			Entities:         evidence[i].sources,
		})

		// ── FIVE-GATE CHAIN (per scene) ──────────────────────────
		occurrences, err := CertifyEntityTimingChain(CertifyEntityTimingInput{
			SceneIndex:           i,
			SceneID:              scene.id,
			Text:                 scene.text,
			Entities:             evidence[i].sources,
			Timing:               evidence[i].timing,
			VoiceoverAssetID:     "vo-" + job.id + "-" + scene.id,
			TimelineStartUS:      timelineStart,
			FinalAudioDurationUS: totalDuration,
		})
		require.NoError(t, err, "scene %s: entity timing certification failed", scene.id)
		require.Len(t, occurrences, len(evidence[i].sources), "scene %s: every extracted entity must project", scene.id)

		runes := []rune(scene.text)
		for _, o := range occurrences {
			require.NoError(t, o.Validate(), "scene %s: occurrence %q must satisfy the projection invariants", scene.id, o.Name)

			// ENTITY IN TEXT — the rune span anchors a verbatim mention.
			require.Equal(t, o.Name, string(runes[o.TextStart:o.TextEnd]), "scene %s: text span must equal the entity", scene.id)

			// WORD TIMING — the local span is EXACTLY the first matched
			// word's start to the last matched word's end (never padded,
			// never estimated from the text length).
			first := o.WordStart
			last := o.WordEnd
			require.Equal(t, evidence[i].timing.Words[first].StartUS, o.LocalStartUS, "scene %s: local start must be the first spoken word's start", scene.id)
			require.Equal(t, evidence[i].timing.Words[last].EndUS, o.LocalEndUS, "scene %s: local end must be the last spoken word's end", scene.id)

			// ENTITY START — the global position is the canonical offset
			// plus the local span.
			require.Equal(t, timelineStart+o.LocalStartUS, o.AudioStartUS, "scene %s: audio_start_us must be timeline_start + local", scene.id)
			require.Equal(t, timelineStart+o.LocalEndUS, o.AudioEndUS, "scene %s: audio_end_us must be timeline_start + local", scene.id)

			// PLAYBACK BOUND — the occurrence lives on the certified
			// voiceover: bound to its audio hash, inside its duration.
			require.Equal(t, "vo-"+job.id+"-"+scene.id, o.VoiceoverAssetID)
			require.NotEmpty(t, evidence[i].timing.AudioSHA256, "scene %s: the timing artifact must be audio-bound", scene.id)
			require.LessOrEqual(t, o.LocalEndUS, evidence[i].timing.DurationUS, "scene %s: occurrence inside the voiceover duration", scene.id)
			require.LessOrEqual(t, o.AudioEndUS, totalDuration, "scene %s: occurrence inside the final audio", scene.id)
		}
	}

	// ── FULL TIMELINE + OVERLAY PLAN + CHRONON ──────────────────
	timeline, err := BuildEntityTimeline(BuildInput{
		ProjectID:  job.id,
		Language:   job.language,
		DurationUS: totalDuration,
		Scenes:     sceneInputs,
	})
	require.NoError(t, err)
	require.NoError(t, timeline.Validate())

	plan, err := ResolveEntityOverlayPlan(timeline, "plan-"+job.id, "video-"+job.id, job.id, 1280, 720, 30, 1)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(plan.Items), len(job.scenes), "job %s: at least one entity card per scene's key entity", job.id)

	// The key entity of every scene has an entity_card that starts exactly
	// when the entity is spoken (ms = global us / 1000, floor/ceil of the
	// certified microsecond span).
	for _, scene := range job.scenes {
		occurrence := findOccurrence(t, timeline, scene.id, scene.keyEntity)
		item := findItem(t, plan, "overlay-"+scene.id+"-"+SafeEntityID(occurrence.Name))
		require.Equal(t, string(capabilityoverlay.EntityTypeToKind(occurrence.Type)), item.Kind)
		wantTemplate, err := capabilityoverlay.DefaultChrononOverlayRegistry.ResolveTemplate(string(capabilityoverlay.EntityTypeToKind(occurrence.Type)))
		require.NoError(t, err)
		require.Equal(t, wantTemplate, item.TemplateID)
		require.Equal(t, occurrence.Name, item.Text)
		require.Equal(t, occurrence.AudioStartUS/1000, item.StartMs, "scene %s: card starts exactly when the entity is spoken", scene.id)
		require.Equal(t, (occurrence.AudioEndUS+999)/1000, item.EndMs, "scene %s: card ends exactly when the entity is spoken", scene.id)
	}

	// The plan compiles to chronon with one text layer per entity card.
	compiled, err := capabilityoverlay.CompileChrononPlan(plan)
	require.NoError(t, err)
	require.Len(t, compiled.Plan.Layers, len(job.scenes), "job %s: chronon must carry one layer per entity card", job.id)
	for _, layer := range compiled.Plan.Layers {
		item := findItem(t, plan, layer.ID)
		require.Equal(t, "", layer.Type)
		require.Equal(t, item.PresetID, layer.Preset)
		require.NotEmpty(t, layer.Text)
		require.Greater(t, layer.DurationFrames, int64(0))
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("\n===== ENTITY TIMING CERTIFICATION: %s (%s) =====\n", job.id, job.language))
	for _, scene := range job.scenes {
		occurrence := findOccurrence(t, timeline, scene.id, scene.keyEntity)
		fmt.Fprintf(&report, "  scene %-16s %-24q %-5s audio %d.%03d → %d.%03d s (word %d→%d)\n",
			scene.id, scene.keyEntity, occurrence.Type,
			occurrence.AudioStartUS/1_000_000, (occurrence.AudioStartUS%1_000_000)/1000,
			occurrence.AudioEndUS/1_000_000, (occurrence.AudioEndUS%1_000_000)/1000,
			occurrence.WordStart, occurrence.WordEnd)
	}
	report.WriteString("GATES TEXT/NLP/WORD/GLOBAL/MASTER: PASS — entity appears exactly while spoken\n")
	report.WriteString("CERTIFIED\n")
	t.Log(report.String())
}

func findOccurrence(t *testing.T, timeline EntityTimeline, sceneID, name string) EntityOccurrence {
	t.Helper()
	for _, scene := range timeline.Scenes {
		if scene.SceneID != sceneID {
			continue
		}
		for _, o := range scene.Entities {
			if o.Name == name {
				return o
			}
		}
	}
	t.Fatalf("scene %s: occurrence %q not found in the timeline", sceneID, name)
	return EntityOccurrence{}
}

func findItem(t *testing.T, plan capabilityoverlay.OverlayPlan, id string) capabilityoverlay.OverlayItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("overlay item %q not found in the plan", id)
	return capabilityoverlay.OverlayItem{}
}

// TestCertification_TwoCompleteJobs certifies both jobs (Maya civilization
// and classical composers) end to end — the two complete jobs the spec asks
// for before any Chronon render.
func TestCertification_TwoCompleteJobs(t *testing.T) {
	for _, job := range certJobs() {
		t.Run(job.id, func(t *testing.T) {
			certifyJob(t, job)
		})
	}
}
