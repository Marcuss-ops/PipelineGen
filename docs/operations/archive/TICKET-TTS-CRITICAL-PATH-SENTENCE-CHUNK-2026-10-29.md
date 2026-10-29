# TICKET — TTS Critical Path: sentence-chunk paradigm active, provider decision

Data: 2026-10-29 · Stato: **CHIUSO (implementato e certificato)**

## Esito in una frase

La corsa TTS non si vince con un nuovo provider, ma con la granularità: il
percorso è ora a **frase**, con la pool allargata sulla misura del giorno, e
il budget certificato scende da ~8 s a **~1,3 s per scena**. Kokoro resta
scartato (già provato, esito negativo qualitativo).

## 1. Diagnosi (dati 1.456 run, `performance_steps` `edge_tts` in media.db.sqlite)

- Sintesi attiva acumulata: 32,9 h · wall TTS: 78,9 h.
- Il wall attivo era **per-scena**: p50 24 s/run, p99 918 s; per call p50 8,0 s
  con corr≈0,87 vs numero di scene (1 richiesta di streaming per scena intera).
- La pool di default (4) era la misura del vecchio carico a **scena intera**;
  il cap esisteva per saturazione client TLS/DNS, non del servizio.
- dove andava già bene: cache per fingerprint v2, `resolve_voice` (zero rete in
  prod), finalize/remove-silence post-TTS (<1 s): nessun intervento.

## 2. Cosa è cambiato (implementato)

| File | Cambio |
|---|---|
| `config.yaml` | `max_concurrent_tts` 4 → 12 (misurato: 0 errori a 24 conc, throughput lineare 1,9→9,7 call/s, latenza stabile) |
| `platform/config/defaults.go` + `resolved.go` | `voiceover.chunk_concurrency` 3 → 12 (fan-out dei chunk per scena) |
| `wiring/voiceover/chunked_voiceover_tts.go` | `voiceoverChunkMaxWords` 400 → **24** (≈ 1 frase): il chunking, prima de-facto inattivo su scene reali (20–40 parole), è ora la granularità di default; cap servizio (400 parole) non toccato |
| idem | **fail-open**: un chunk fallito degrada alla sintesi scena-intera (contratto invariato, path di ripristino con retry/loudness-gate/empty-audio repair) |
| `wiring/voiceover/chunked_voiceover_tts_test.go` | test allineati alla nuova soglia + pin del fail-open (`FallsBackToWholeSceneOnChunkFailure`) |

La scena tipica (2–4 frasi) ora sintetizza le frasi in parallelo e il collo di
per-scena diventa `max(frase) + merge boundary` (offset remap già
implementato e pinsato dai test esistenti).

## 3. Golden job e budget

`ops/jobs/tts_sentence_golden_20261029/run_golden.sh` — contro il bridge live:
- 3 scene × 3 frasi, fan-out 12, misure wall/latenze e validatori artefatti;
- esito certificato odierno: **critico per scena 0,98–1,32 s**, chunk p50 0,98 s,
  0 errori, wall 1,32 s su budget 10 s → **PASS**;
- budget pinnato: streak `wall ≤ 10 s`, `chunk p50 ≤ 4 s` (variabile
  `TTS_GOLDEN_BUDGET_S`).

Stima per il percorso critico: ~24 s → **~3–5 s** alla granularità frase con
scena non ancora pronta (streaming overlap), ~1,3 s quando la scena è già
pronta al trigger.

## 4. Decisione provider

- **Edge TTS (attivo)**: gratuito, streaming word-boundary nella stessa passata,
  saturazione reale non raggiunta fino a 24 call parallele. La leva è
  granularità + pool, non sostituzione.
- **Kokoro (scartato — già provato)**: sperimentato in passato con esito
  negativo qualitativo; non va re-riesaminato senza un cambiamento di versione
  del modello. Eventuali future candidature (Piper, provider gestito
  streaming con TTFB < 500 ms) passeranno dal ticket, non dal codice.
- Conseguenza architetturale: il provider resta dietro `TTSProvider` +
  `chunkedTTSProvider`; ogni swap futuro non tocca né il merge boundary né il
  fail-open (che restano i due invarianti certificati).

## 5. Verifiche

- `go build ./internal/...` PASS;
- `go test ./internal/app/wiring/voiceover/ -run 'TestChunked|TestSplit'` PASS;
- golden job PASS (vedi §3);
- restart-verify del servizio a completamento dell'ondata.
