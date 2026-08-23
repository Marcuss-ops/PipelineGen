// Package scripts — fixtures_real_scripts_test.go (PR-CS-1, FASE 9, DoD #10).
//
// 4 canonical real-script fixtures (sport/news/biography/tutorial) and
// 4 matching TestFixture_* tests. Each fixture is an Italian-language
// ScriptSegment array populated with real, verifiable facts (dates,
// names, verdicts, stats, commands). The fixtures are the canonical
// ground truth for DoD #10 — downstream live battery tests (FASE 10)
// consume these same structures verbatim, so any drift here would
// silently invalidate the live battery assertions.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// 4 fixture arrays. No other test file is allowed to redeclare
// `fixtureSportBoxing` / `fixtureNewsGossip` / `fixtureBiography` /
// `fixtureTutorial`. Future cross-language fixtures must append to
// this file, not duplicate it.
//
// Italian content chosen because production voiceover is IT-first
// (the canonical deployment target). Topical facts (Pacquiao-Broner
// 2019, Apple Q1 2024, Steve Jobs, yt-dlp CLI) are intentionally
// factual so a future-investigator can cross-check the source_text
// content against public sources if a regression is reported.
//
// Each TestFixture_* locks 4 axes at the package boundary:
//  1. All segments are present in the prompt in input order
//     (SEGMENT 1..N markers followed by their Topic).
//  2. Each segment's source_text appears verbatim in the rendered
//     prompt (no paraphrasing, no lossy rendering).
//  3. The fallback-chain target resolves to a positive integer
//     when the plan carries a per-segment TargetWords (FASE 5
//     helper effectiveTargetForBudgetWords).
//  4. The fingerprint is stable across two distinct calls with
//     the same fixture — FASE 7 forward-pin.
//
// No model invocation: the test deliberately stops at "the
// prompt is correct + the fingerprint is deterministic". Live
// battery execution against Ollama lives in FASE 10 and is
// explicitly out of scope here.
//
// godlike/07 minimum blast radius: zero production code changes.
package usecase

import (
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Fixture 1: SPORT — Pacquiao vs Broner, 19 gennaio 2019 ──────────

// fixtureSportBoxing è la cronologia dell'incontro WBA welterweight
// del 19 gennaio 2019 al MGM Grand Garden Arena di Las Vegas. Sei
// segmenti tematici (apertura, tre round raggruppati, decisione,
// significato). Ogni segmento cita fatti reali e verificabili
// (date, verdetti, statistiche, nomi).
var fixtureSportBoxing = [...]scriptpkg.ScriptSegment{
	{
		Topic:       "Apertura",
		SourceText:  "Il 19 gennaio 2019 al MGM Grand Garden Arena di Las Vegas, Manny Pacquiao ha difeso il titolo WBA dei pesi welter contro Adrien Broner. Pacquiao aveva 40 anni, Broner 29.",
		TargetWords: 90,
	},
	{
		Topic:       "Round 1-3",
		SourceText:  "Nei primi tre round Broner ha impostato una guardia chiusa e una strategia di sopravvivenza. Pacquiao ha studiato il ritmo, toccando con jab tesi senza forzare. Nessun knockdown nei primi tre round.",
		TargetWords: 110,
	},
	{
		Topic:       "Round 4-5",
		SourceText:  "Dal quarto round Pacquiao ha alzato il ritmo: combinazione sinistra-destra al quarto round, serie al corpo al quinto. Broner ha risposto con single jab ma il volume era nettamente a favore di Pacquiao.",
		TargetWords: 100,
	},
	{
		Topic:       "Round 6-12",
		SourceText:  "Nei round centrali Pacquiao ha mantenuto il volume, piazzando il 60% dei colpi significativi. Broner ha tentato il contrattacco solo a tratti, senza mai scuotere il filippino. Nessun knockdown registrato in questi sette round.",
		TargetWords: 120,
	},
	{
		Topic:       "Decisione",
		SourceText:  "Al termine delle dodici riprese i giudici hanno dato verdetto unanime a favore di Pacquiao con i cartellini 117-110, 116-111, 116-111. Pacquiao conserva il titolo WBA welter.",
		TargetWords: 80,
	},
	{
		Topic:       "Significato",
		SourceText:  "La vittoria conferma Pacquiao tra i contendenti della divisione welter e apre l'ipotesi di un match unificato con Errol Spence Jr, poi concretizzato il 21 agosto 2021.",
		TargetWords: 70,
	},
}

// ── Fixture 2: NEWS — Apple Q1 2024 earnings, 1 febbraio 2024 ────

// fixtureNewsGossip è una notizia economica (trimestrale Apple Q1
// 2024) strutturata in cinque segmenti: contesto macro, evento
// principale (annuncio risultati), reazioni del mercato,
// conseguenze operative, prospettive. Cifre da comunicato stampa
// ufficiale Apple del 1 febbraio 2024.
var fixtureNewsGossip = [...]scriptpkg.ScriptSegment{
	{
		Topic:       "Contesto macro",
		SourceText:  "Il primo trimestre fiscale 2024 di Apple copre ottobre-dicembre 2023. Il mercato smartphone globale aveva registrato una contrazione del 3% nel 2023 secondo IDC. Apple partiva da un ciclo di sostituzione iPhone in attenuazione.",
		TargetWords: 90,
	},
	{
		Topic:       "Evento principale",
		SourceText:  "Il 1 febbraio 2024 Apple ha annunciato ricavi record per il trimestre Q1 FY24: 124,3 miliardi di dollari, in crescita del 2% anno su anno. EPS diluito a 2,10 dollari. Il fatturato servizi ha raggiunto 24,3 miliardi di dollari.",
		TargetWords: 110,
	},
	{
		Topic:       "Reazioni del mercato",
		SourceText:  "Il titolo AAPL ha chiuso in lieve ribasso nel trading after-hours del 1 febbraio 2024 nonostante il battuto iPhone. Gli analisti di Morgan Stanley hanno confermato rating overweight con target price 220 dollari.",
		TargetWords: 90,
	},
	{
		Topic:       "Conseguenze operative",
		SourceText:  "Tim Cook ha annunciato investimenti accelerati in Apple Intelligence, la piattaforma AI generativa proprietaria. Le spese in conto capitale sono previste in crescita nel 2024. La divisione Greater China ha registrato un calo nelle vendite iPhone.",
		TargetWords: 100,
	},
	{
		Topic:       "Prospettive",
		SourceText:  "Il lancio di Apple Vision Pro, avvenuto il 2 febbraio 2024 negli Stati Uniti, è il primo prodotto di una nuova categoria. Cook ha dichiarato: We are pleased to report record quarterly revenue, driven by strong performance in Services.",
		TargetWords: 80,
	},
}

// ── Fixture 3: BIOGRAPHY — Steve Jobs, 1955-2011 ────────────────────

// fixtureBiography è la biografia essenziale di Steve Jobs in cinque
// segmenti: infanzia, carriera iniziale, svolta (il ritorno ad
// Apple), fase difficile (1985-1997), conclusione. Date da
// biografie pubbliche (Walter Isaacson 2011).
var fixtureBiography = [...]scriptpkg.ScriptSegment{
	{
		Topic:       "Infanzia",
		SourceText:  "Steven Paul Jobs è nato il 24 febbraio 1955 a San Francisco. Fu adottato da Paul Reinhold Jobs e Clara Hagopian Jobs, una coppia di operai. Crebbe a Mountain View, nel cuore della futura Silicon Valley, e frequentò il Homestead High School.",
		TargetWords: 90,
	},
	{
		Topic:       "Carriera iniziale",
		SourceText:  "Jobs abbandonò il Reed College dopo un semestre. Il 1 aprile 1976 fondò Apple Computer insieme a Steve Wozniak e Ronald Wayne nel garage dei genitori a Los Altos. Il primo prodotto fu Apple I, poi Apple II nel giugno 1977.",
		TargetWords: 110,
	},
	{
		Topic:       "Svolta",
		SourceText:  "Il ritorno di Jobs in Apple nel 1997 dopo l'acquisizione di NeXT segna la svolta. Il keynote del 9 gennaio 2007 al Macworld presentò il primo iPhone. La strategia di integrazione hardware-software ha ridefinito l'industria mobile.",
		TargetWords: 110,
	},
	{
		Topic:       "Fase difficile",
		SourceText:  "Jobs fu licenziato da Apple il 17 settembre 1985 dal consiglio di amministrazione guidato da John Sculley. Fondò NeXT Computer e acquistò Pixar da Lucasfilm nel 1986 per 5 milioni di dollari, poi portata in borsa nel 1995.",
		TargetWords: 100,
	},
	{
		Topic:       "Conclusione",
		SourceText:  "Jobs si dimise da CEO di Apple il 24 agosto 2011, lasciando la guida a Tim Cook. Morì il 5 ottobre 2011 a Palo Alto, in California, per complicanze di un tumore neuroendocrino pancreatico diagnosticato nel 2003.",
		TargetWords: 90,
	},
}

// ── Fixture 4: TUTORIAL — yt-dlp CLI, download video da YouTube ────

// fixtureTutorial è un tutorial tecnico su yt-dlp (fork attivo di
// youtube-dl) strutturato in sei segmenti: introduzione,
// prerequisiti, tre passi canonici, verifica finale, troubleshooting.
// Comandi reali e output atteso basati sulla documentazione ufficiale
// yt-dlp 2024.
var fixtureTutorial = [...]scriptpkg.ScriptSegment{
	{
		Topic:       "Introduzione",
		SourceText:  "yt-dlp è un fork attivo di youtube-dl che permette di scaricare video e audio da YouTube e oltre 1500 siti supportati. Il progetto è ospitato su GitHub all'organizzazione yt-dlp/yt-dlp ed è rilasciato sotto licenza Unlicense.",
		TargetWords: 80,
	},
	{
		Topic:       "Prerequisiti",
		SourceText:  "yt-dlp richiede Python 3.8 o superiore e FFmpeg disponibile nel PATH per fusione audio-video e conversione formati. Su Debian/Ubuntu installare con: sudo apt install python3 ffmpeg. Su macOS con Homebrew: brew install python ffmpeg.",
		TargetWords: 100,
	},
	{
		Topic:       "Passo 1 installazione",
		SourceText:  "Installare yt-dlp via pip con il comando: pip install yt-dlp. Per restare sempre aggiornati aggiungere: pip install -U yt-dlp. In alternativa, scaricare il binario standalone da github.com/yt-dlp/yt-dlp/releases/latest.",
		TargetWords: 90,
	},
	{
		Topic:       "Passo 2 download base",
		SourceText:  "Scaricare un video con il comando canonico: yt-dlp \"URL_DEL_VIDEO\". Il file viene salvato nella directory corrente con nome del titolo del video ed estensione originale. Esempio: yt-dlp \"https://www.youtube.com/watch?v=dQw4w9WgXcQ\".",
		TargetWords: 100,
	},
	{
		Topic:       "Passo 3 formato specifico",
		SourceText:  "Per selezionare un formato specifico, prima elencare i formati disponibili con: yt-dlp -F \"URL\". Quindi scaricare il formato scelto con: yt-dlp -f 137+140 \"URL\" (unisce video 1080p e audio AAC). Il flag --merge-output-format mkv produce un contenitore unico.",
		TargetWords: 110,
	},
	{
		Topic:       "Troubleshooting",
		SourceText:  "Errore 403 Forbidden: aggiornare yt-dlp con pip install -U yt-dlp e riprovare. Restrizioni geografiche: aggiungere --geo-bypass o usare un proxy. Errori di rete intermittenti: aumentare i tentativi con --retries 10 oppure --fragment-retries 10.",
		TargetWords: 100,
	},
}

// ── Helper: plan factory from a fixture ────────────────────────────

// makePlanFromFixture costruisce un ResolvedGenerationPlan con i
// campi canonici pre-compilati e i segmenti della fixture. Tutti
// i 4 test condividono questa factory per restare coerenti nel
// linguaggio (it), tone (documentary) e modello (llama3:8b).
func makePlanFromFixture(title string, segs []scriptpkg.ScriptSegment) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:         title,
		Topic:         title,
		Language:      "it",
		Tone:          "documentary",
		Model:         "llama3:8b",
		SourceKind:    "text",
		PromptVersion: "v1",
		PromptProfile: "fixture-v1",
		Segments:      segs,
	}
}

// ── Test 1: Sport (Boxing) ────────────────────────────────────────

func TestFixture_Boxing_AllSegmentsPresentOrderedAndFactsPreserved(t *testing.T) {
	t.Parallel()
	plan := makePlanFromFixture("Pacquiao vs Broner", fixtureSportBoxing[:])
	prompt := buildSegmentInstructions(plan)
	if prompt == "" {
		t.Fatal("DoD #10: prompt MUST be non-empty for sport fixture")
	}
	assertAllSegmentsPresentInOrder(t, "Pacquiao vs Broner", prompt, fixtureSportBoxing[:])
	assertEachSourceTextVerbatim(t, "Pacquiao vs Broner", prompt, fixtureSportBoxing[:])
	assertFallbackChainResolves(t, "Pacquiao vs Broner", plan, 90)
	assertFingerprintStable(t, "Pacquiao vs Broner", plan)
}

// ── Test 2: News ──────────────────────────────────────────────────

func TestFixture_News_AllSegmentsPresentOrderedAndFactsPreserved(t *testing.T) {
	t.Parallel()
	plan := makePlanFromFixture("Apple Q1 FY2024 Earnings", fixtureNewsGossip[:])
	prompt := buildSegmentInstructions(plan)
	if prompt == "" {
		t.Fatal("DoD #10: prompt MUST be non-empty for news fixture")
	}
	assertAllSegmentsPresentInOrder(t, "Apple Q1 FY2024 Earnings", prompt, fixtureNewsGossip[:])
	assertEachSourceTextVerbatim(t, "Apple Q1 FY2024 Earnings", prompt, fixtureNewsGossip[:])
	assertFallbackChainResolves(t, "Apple Q1 FY2024 Earnings", plan, 90)
	assertFingerprintStable(t, "Apple Q1 FY2024 Earnings", plan)
}

// ── Test 3: Biography ─────────────────────────────────────────────

func TestFixture_Biography_AllSegmentsPresentOrderedAndFactsPreserved(t *testing.T) {
	t.Parallel()
	plan := makePlanFromFixture("Steve Jobs (1955-2011)", fixtureBiography[:])
	prompt := buildSegmentInstructions(plan)
	if prompt == "" {
		t.Fatal("DoD #10: prompt MUST be non-empty for biography fixture")
	}
	assertAllSegmentsPresentInOrder(t, "Steve Jobs", prompt, fixtureBiography[:])
	assertEachSourceTextVerbatim(t, "Steve Jobs", prompt, fixtureBiography[:])
	assertFallbackChainResolves(t, "Steve Jobs", plan, 90)
	assertFingerprintStable(t, "Steve Jobs", plan)
}

// ── Test 4: Tutorial ──────────────────────────────────────────────

func TestFixture_Tutorial_AllSegmentsPresentOrderedAndFactsPreserved(t *testing.T) {
	t.Parallel()
	plan := makePlanFromFixture("yt-dlp CLI tutorial", fixtureTutorial[:])
	prompt := buildSegmentInstructions(plan)
	if prompt == "" {
		t.Fatal("DoD #10: prompt MUST be non-empty for tutorial fixture")
	}
	assertAllSegmentsPresentInOrder(t, "yt-dlp tutorial", prompt, fixtureTutorial[:])
	assertEachSourceTextVerbatim(t, "yt-dlp tutorial", prompt, fixtureTutorial[:])
	assertFallbackChainResolves(t, "yt-dlp tutorial", plan, 80)
	assertFingerprintStable(t, "yt-dlp tutorial", plan)
}

// ── Shared assertion helpers (4 axes per DoD #10) ─────────────────

// assertAllSegmentsPresentInOrder pin axis 1: every segment has a
// monotonically increasing SEGMENT N marker and the matching
// "Topic: <Topic>" line appears AFTER its marker.
func assertAllSegmentsPresentInOrder(t *testing.T, label string, prompt string, segs []scriptpkg.ScriptSegment) {
	t.Helper()
	prevIdx := -1
	for i, s := range segs {
		marker := fmt.Sprintf("SEGMENT %d", i+1)
		idx := strings.Index(prompt, marker)
		if idx < 0 {
			t.Errorf("[%s] DoD #10 axis 1: marker %q MUST be present in prompt", label, marker)
			continue
		}
		if idx <= prevIdx {
			t.Errorf("[%s] DoD #10 axis 1: marker %q MUST appear after the previous marker (position %d <= %d); segment order lost",
				label, marker, idx, prevIdx)
		}
		// Assert the matching Topic appears after the marker.
		after := prompt[idx:]
		wantTopic := "Topic: " + s.Topic
		if !strings.Contains(after, wantTopic) {
			t.Errorf("[%s] DoD #10 axis 1: %q MUST be followed by %q in the prompt", label, marker, wantTopic)
		}
		prevIdx = idx
	}
}

// assertEachSourceTextVerbatim pin axis 2: each segment's
// source_text appears verbatim in the rendered prompt (no
// lossy rendering, no paraphrasing). The fixtures intentionally
// use distinctive citations (dates, scores, commands) so a
// substring match is unambiguous.
func assertEachSourceTextVerbatim(t *testing.T, label string, prompt string, segs []scriptpkg.ScriptSegment) {
	t.Helper()
	for i, s := range segs {
		if strings.TrimSpace(s.SourceText) == "" {
			continue
		}
		if !strings.Contains(prompt, s.SourceText) {
			t.Errorf("[%s] DoD #10 axis 2: segment[%d].SourceText (%d chars) MUST appear verbatim in prompt; got prompt:\n%s",
				label, i, len(s.SourceText), prompt)
		}
	}
}

// assertFallbackChainResolves pin axis 3: the per-segment target
// resolves through effectiveTargetForBudgetWords. The canonical
// chain is per-segment > SegmentWords > TargetWords > 80;
// every fixture has segment[0].TargetWords > 0 so the first
// branch wins. expected is the per-segment[0] value declared
// in the fixture itself.
func assertFallbackChainResolves(t *testing.T, label string, plan *scriptpkg.ResolvedGenerationPlan, expected int) {
	t.Helper()
	got := effectiveTargetForBudgetWords(plan)
	if got <= 0 {
		t.Errorf("[%s] DoD #10 axis 3: fallback chain MUST resolve to a positive int; got %d", label, got)
	}
	if got != expected {
		t.Errorf("[%s] DoD #10 axis 3: first-segment target expected %d (per-segment chain wins), got %d", label, expected, got)
	}
}

// assertFingerprintStable pin axis 4: BuildFingerprint is
// deterministic across two distinct calls with the same plan
// (FASE 7 forward-pin). Both calls use the canonical
// FingerprintInputFromPlan(plan) → BuildFingerprint chain.
func assertFingerprintStable(t *testing.T, label string, plan *scriptpkg.ResolvedGenerationPlan) {
	t.Helper()
	in1 := scriptpkg.FingerprintInputFromPlan(plan)
	in2 := scriptpkg.FingerprintInputFromPlan(plan)
	fp1 := scriptpkg.BuildFingerprint(in1)
	fp2 := scriptpkg.BuildFingerprint(in2)
	if fp1 == "" || fp1 == "ffffffffffffffff" {
		t.Errorf("[%s] DoD #10 axis 4: fingerprint MUST be a real 16-hex hash; got %q", label, fp1)
	}
	if fp1 != fp2 {
		t.Errorf("[%s] DoD #10 axis 4: fingerprint NOT stable across 2 calls (%q vs %q); build is non-deterministic",
			label, fp1, fp2)
	}
	// Length sanity — BuildFingerprint returns first 16 hex chars
	// of SHA-256 (FASE 7 implementation per fingerprint.go).
	if len(fp1) != 16 {
		t.Errorf("[%s] DoD #10 axis 4: fingerprint length MUST be 16 hex chars; got %d (%q)", label, len(fp1), fp1)
	}
}
