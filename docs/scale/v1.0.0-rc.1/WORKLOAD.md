# PR8 — Workload Definition v1.0.0-rc.1

## Stime basate sul codebase (non validate con staging)

Le stime sotto sono derivate dall'analisi del codebase (667 file Go, 22 job type, architettura single-server + single-worker) e vanno validate con almeno 7 giorni di metriche in staging.

| Metrica | Stima iniziale | Target 2× | Note |
|---|---:|---:|---|
| canali gestiti | ~50 | 100 | ChannelMonitor configurabile per canale |
| video/giorno | ~20 | 40 | yt-dlp limitato da rate-limit YouTube |
| job/ora picco | ~15 | 30 | Mix di extraction, enrichment, indexing |
| job concorrenti | 1 video extract + 1 ollama | 2+2 | `MaxConcurrentVideoExtracts`, `MaxConcurrentOllamaCalls` |
| richieste API/s | ~5 read | 10 | API read-heavy (status, search); write via async job |
| clip generate/giorno | ~50 | 100 | FFmpeg-bound, ~3 segment per video |
| upload Drive/giorno | ~50 | 100 | Idempotente con retry |
| query Qdrant/s | ~2 | 5 | Ricerca semantica e hybrid search |
| dimensione DB/mese | ~100MB | 200MB | Principalmente `media_assets` + tabelle cache |
| storage locale/mese | ~5GB | 10GB | Clip MP4 temporanei prima dell'upload Drive |

## Limiti esterni identificati

| Provider | Limite | Impatto |
|----------|--------|---------|
| YouTube (yt-dlp) | Rate-limit ~429; cookie expiring | Extraction lento, retry con backoff |
| Google Drive | 750GB/giorno upload; 429 rate-limit | Upload idempotente, retry |
| Ollama | CPU-bound, ~30-60s per generazione metadata | Bottleneck principale per enrichment |
| Qdrant | In-memory, scala verticalmente | Non bottleneck a <100K punti |
| SQLite | Single-writer, WAL mode | Bottleneck a >4 worker concorrenti |
| FFmpeg | CPU-bound, ~10-60s per segment | Bottleneck extraction |
| Artlist scraper | Browser Chromium ~3.5GB RAM | Isolato in container separato |

## Job Type più costosi (stimati)

| Job Type | Durata stimata | Risorse |
|----------|---------------|---------|
| `youtube_clip.extract` | 2-10 min | FFmpeg CPU, yt-dlp I/O, Drive upload |
| `script.generate_from_clips` | 5-15 min | Ollama (LLM), Qdrant search |
| `media.extract` (Artlist) | 1-3 min | FFmpeg, scraper |
| `books.process` | 1-5 min | Ollama, Drive |
| `youtube.rebuild_search_text` | 1-3 min | Ollama, batch DB |
| `qdr.prewarm` | 1-5 min | Qdrant + SQLite warmup |

## Prossimi passi

- [ ] Raccogliere metriche reali da staging per 7 giorni
- [ ] Validare le stime con dati di produzione
- [ ] Misurare p50/p95/p99 per ogni job type
