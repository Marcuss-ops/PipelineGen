package overlays

import (
	"strings"
	"testing"
)

// Regression coverage for the 2026-10-19 live run failure:
// phrase_apple_clean_12_slide_diagonal_pop is certified on short catalog
// input only, but historical payloads (garlasco-longcat-68-b, probe-long-6*)
// carry it in their explicit phrase_motions pools. On long copy the render
// daemon rejects its position keyframes and fails the whole run, so the
// long-copy verbatim paths must filter it instead of dealing it.

const longCopyCrashProbeMotion = "phrase_apple_clean_12_slide_diagonal_pop"

func inLongPool(id string) bool {
	for _, candidate := range longPhraseMotionCandidates {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestLongLaneNeverDealsCrashingMotionFromExplicitPool(t *testing.T) {
	validLong := longPhraseMotionCandidates[0]
	pools := map[string][]string{
		"singleton": {longCopyCrashProbeMotion},
		"mixed":     {longCopyCrashProbeMotion, validLong},
	}
	for name, pool := range pools {
		for ordinal := 0; ordinal < 2*len(longPhraseMotionCandidates); ordinal++ {
			for _, limit := range []int{0, 5} {
				got := selectLongPhraseMotionLimited("long-guard", "run", ordinal, pool, limit)
				if got == longCopyCrashProbeMotion {
					t.Fatalf("%s pool dealt crashing motion at ordinal %d limit %d", name, ordinal, limit)
				}
				if got == "" {
					t.Fatalf("%s pool returned empty motion at ordinal %d limit %d", name, ordinal, limit)
				}
			}
		}
	}
}

func TestLongLaneCrashingOnlyPoolFallsBackToWhitelist(t *testing.T) {
	got := selectLongPhraseMotionLimited("long-guard", "run", 0, []string{longCopyCrashProbeMotion}, 0)
	if got == longCopyCrashProbeMotion {
		t.Fatal("crashing-only pool still dealt the crashing motion")
	}
	if !inLongPool(got) {
		t.Fatalf("crashing-only pool fell back to %q, want the long-copy whitelist", got)
	}
}

func longCopyGuardPhrase(text, role string) TimedAnnotation {
	return TimedAnnotation{
		Text: text, StartMs: 0, EndMs: 8000, StartUS: 0, DurationUS: 8_000_000,
		Score: 0.9, PresentationRole: role,
	}
}

const longCopyGuardText = "La chiusura delle indagini precede una richiesta di processo ma non equivale mai a una condanna definitiva"

func TestPlannerSingletonCrashingMotionFallsBackOnLongCopy(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "long-guard-plan", VideoID: "video-long-guard",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions: []string{longCopyCrashProbeMotion},
		Scenes: []SceneInput{{ID: "s1", Phrases: []TimedAnnotation{
			longCopyGuardPhrase(longCopyGuardText, "scene_bullet"),
		}}},
	}, PlannerConfig{MaxPhrases: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("plan items = %d, want 1", len(plan.Items))
	}
	if plan.Items[0].MotionID == longCopyCrashProbeMotion {
		t.Fatal("planner singleton dealt the crashing motion to long copy")
	}
	if !inLongPool(plan.Items[0].MotionID) {
		t.Fatalf("singleton fallback = %q, want the long-copy whitelist", plan.Items[0].MotionID)
	}
}

func TestPlannerSingletonCrashingMotionStaysVerbatimOnShortCopy(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "long-guard-short", VideoID: "video-long-guard-short",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions: []string{longCopyCrashProbeMotion},
		Scenes: []SceneInput{{ID: "s1", Phrases: []TimedAnnotation{
			longCopyGuardPhrase("Crollo dei margini finali", "scene_title"),
		}}},
	}, PlannerConfig{MaxPhrases: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("plan items = %d, want 1", len(plan.Items))
	}
	if plan.Items[0].MotionID != longCopyCrashProbeMotion {
		t.Fatalf("short-copy singleton = %q, want verbatim %q", plan.Items[0].MotionID, longCopyCrashProbeMotion)
	}
}

func TestHeavyLaneNeverDealsCrashingMotionFromExplicitPool(t *testing.T) {
	for _, limit := range []int{0, 5} {
		got := selectHeavyPhraseMotionLimited("long-guard", "run", 0, []string{longCopyCrashProbeMotion}, limit)
		if got == longCopyCrashProbeMotion {
			t.Fatalf("heavy lane dealt crashing motion with limit %d", limit)
		}
		if got == "" {
			t.Fatalf("heavy lane returned empty motion with limit %d", limit)
		}
	}
}

func TestShortLaneExplicitPoolUnchanged(t *testing.T) {
	got := selectShortPhraseMotionLimited("long-guard", "scene", 0, 3, []string{longCopyCrashProbeMotion}, 0)
	if got == "" {
		t.Fatal("short lane returned empty motion for an explicit crashing-id pool")
	}
	if got == longCopyCrashProbeMotion {
		t.Fatal("short lane dealt the crashing motion: blocked_lanes regression")
	}
	if !AnimationLaneCompatible(got, "short") {
		t.Fatalf("short lane = %q, want a short-lane-compatible entrance", got)
	}
}

// TestHistoricalLongcatPoolNeverDealsCrashingMotion replays the exact
// explicit phrase_motions pool of the garlasco-longcat-68-b-20261014 payload
// (whose long-copy rotation crashed the render daemon live on 2026-10-19)
// over 20 long phrases: no item may carry the crashing motion, and every
// long item must stay inside the long-copy whitelist.
func TestHistoricalLongcatPoolNeverDealsCrashingMotion(t *testing.T) {
	// The verbatim payload pool minus its three typewriter entries, which the
	// registry retired after 2026-10-14: validation now fails those closed at
	// plan time (covered by TestRetiredShortPhraseRecipeIsRefusedAsExplicitPolicy),
	// so the replay keeps the 17 still-certified entries with the crashing id.
	pool := []string{
		"phrase_apple_clean_09_slide_down_catch", "phrase_apple_clean_11_slide_left_ease",
		"phrase_apple_clean_12_slide_diagonal_pop", "phrase_apple_clean_13_scale_soft_pop",
		"phrase_apple_clean_15_scale_hero_focus", "phrase_apple_clean_16_scale_line_build",
		"phrase_apple_clean_17_scale_in_place", "phrase_apple_clean_18_scale_card_tilt",
		"phrase_apple_clean_19_tracking_tighten", "phrase_apple_clean_20_tracking_spread_clean",
		"phrase_apple_clean_21_tracking_magnetic", "phrase_apple_clean_22_tracking_word_focus",
		"phrase_apple_clean_23_tracking_precision_lock", "phrase_apple_clean_24_tracking_soft_kinetic",
		"phrase_apple_clean_26_opacity_depth_push", "phrase_apple_clean_27_opacity_parallax",
		"phrase_apple_clean_02_blur_focus_snap",
	}
	texts := []string{
		"La chiusura delle indagini precede una richiesta di processo ma non equivale mai a condanna",
		"La seconda chiusura segue accertamenti che le parti potranno contestare con proprie consulenze tecniche",
		"Lindagato ha dichiarato in televisione di non riconoscersi nelle ricostruzioni investigative dei magistrati",
		"Consulenti indipendenti accesso agli atti e contraddittorio restano essenziali per ogni accertamento tecnico",
		"La traccia controversa si discute per attribuzione e qualita senza trasformarla in certezza narrativa",
		"Nessun singolo reperto dimostra da solo una responsabilita servono contesto e verifiche incrociate collegiali",
		"Le relazioni riportate dalla stampa vanno distinte dagli atti ufficiali depositati e verificabili",
		"Il ritrovamento di materiali darchivio non dimostra da solo dolo occultamento o responsabilita penale",
		"La memoria della vittima merita un racconto centrato sulla persona senza dettagli morbosi ne spettacolarizzazione",
		"La vittima non va ridotta a un nome negli atti ne raccontata con biografie non verificate",
		"Le prime indagini distinsero reperti consulenze interpretazioni e decisioni con il metodo di allora",
		"Le sentenze definitive vanno separate dalle nuove ipotesi che restano tesi contestate fino a prova contraria",
		"Il contraddittorio produce consulenze opposte che solo un giudice terzo potra valutare nel merito",
		"Ogni affermazione del copione e verificata sulle fonti processuali e sulle agenzie di stampa",
		"Il racconto si limita ai fatti documentati e nessuna ipotesi viene presentata come certezza",
		"La vicenda resta un caso complesso tra sentenze revisioni e nuove verifiche ancora in corso",
		"Le testimonianze compongono un quadro frammentato da ordinare con cronologia e attribuzione delle fonti",
		"Le consulenze successive hanno riletto i reperti con strumenti moderni con risultati da contestualizzare con cautela",
		"Il deposito delle memorie entro la scadenza chiude una fase senza anticipare la decisione successiva",
		"La procura valuta processo archiviazione o integrazioni e ogni strada resta aperta fino allatto formale",
	}
	scenes := make([]SceneInput, 0, len(texts))
	for i, text := range texts {
		if len(strings.Fields(text)) < 6 {
			t.Fatalf("replay text %d has %d words, want long copy", i, len(strings.Fields(text)))
		}
		// Stagger the windows: identical windows would trip the default
		// per-instant overlap budget and drop phrases before the motion
		// lane ever sees them.
		phrase := longCopyGuardPhrase(text, "scene_bullet")
		phrase.StartMs = int64(i * 8000)
		phrase.EndMs = int64((i + 1) * 8000)
		phrase.StartUS = int64(i * 8_000_000)
		phrase.DurationUS = 8_000_000
		scenes = append(scenes, SceneInput{ID: "s" + string(rune('a'+i)), Phrases: []TimedAnnotation{
			phrase,
		}})
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "longcat-68-b-replay", VideoID: "video-longcat-68-b-replay",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions: pool, HeavyPhrasePriority: 9999,
		Scenes: scenes,
		// The historical payload raised the run-level phrase ceiling to the
		// hard maximum (max_phrase_overlays 20); the certified default is 5.
	}, PlannerConfig{MaxPhrases: 20, RunLevelPhraseOverlayLimit: MaxPhraseOverlaysHardLimit})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != len(texts) {
		t.Fatalf("plan items = %d, want %d", len(plan.Items), len(texts))
	}
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		if item.MotionID == longCopyCrashProbeMotion {
			t.Fatalf("replay dealt crashing motion to item %s (%q)", item.ID, item.Text)
		}
		if !inLongPool(item.MotionID) {
			t.Fatalf("replay item %s = %q, want the long-copy whitelist", item.ID, item.MotionID)
		}
	}
}

func TestUniquenessGuardFallsBackFromCrashingOnlyPool(t *testing.T) {
	validLong := longPhraseMotionCandidates[0]
	items := []OverlayItem{
		{ID: "a", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: longCopyGuardText, MotionID: validLong},
		{ID: "b", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: strings.Replace(longCopyGuardText, "chiusura", "riapertura", 1), MotionID: validLong},
	}
	EnsureDistinctPhraseMotions(items, PlanInput{
		PlanID: "long-guard", VideoID: "video-long-guard",
		PhraseMotions: []string{longCopyCrashProbeMotion},
	})
	for _, item := range items {
		if item.MotionID == longCopyCrashProbeMotion {
			t.Fatalf("guard resolved duplicate %s into the crashing motion", item.ID)
		}
	}
	if items[0].MotionID == items[1].MotionID {
		t.Fatalf("guard left duplicate motion %q on both items", items[0].MotionID)
	}
}
