# PR8 — Environment & SLO v1.0.0-rc.1

## Ambiente di staging richiesto

| Voce | Valore target | Attuale |
|------|--------------|---------|
| OS e architettura | Linux x86_64 | Ubuntu 22.04 |
| CPU e RAM | 8+ core, 16GB+ RAM (CPU-only) | Da verificare |
| spazio disco | 50GB+ | Da verificare |
| Docker version | Docker Compose v2 | Installato ✅ |
| Go version | go1.25+ | go1.25.9 ✅ |
| SQLite version | 3.x con WAL | Disponibile |
| Qdrant version | latest | qdrant/qdrant:latest (270MB) |
| yt-dlp version | 2026.06+ | 2026.06.09 ✅ |
| FFmpeg version | 4.x+ | 4.4.2 ✅ |
| Node version | v22+ | v22.22.2 ✅ |
| Ollama | qualsiasi versione CPU-only | Da verificare |

## SLO (Service Level Objectives)

Basati sui requisiti PR8 con target misurabili:

### API
| Metrica | Target | Finestra |
|---------|--------|----------|
| API availability | >= 99.5% | 30 giorni |
| p95 enqueue latency | < 500 ms | 5 min |
| p95 status read latency | < 300 ms | 5 min |
| p95 lightweight job start | < 30 s | 5 min |

### Job
| Metrica | Target |
|---------|--------|
| job completion success | >= 99.0% (esclusi errori input) |
| job duplicate completion | = 0 |
| job lost | = 0 |
| p95 `youtube_clip.extract` | < 10 min |
| p95 `script.generate_from_clips` | < 15 min |

### Worker
| Metrica | Target |
|---------|--------|
| worker recovery after restart | < 2 lease windows |
| worker heartbeat age | < 60s |

### Backup/Recovery
| Metrica | Target |
|---------|--------|
| backup restore RTO | < 1 ora |
| backup restore RPO | < 1 ora |
| `PRAGMA integrity_check` | sempre OK |

### Error budget
| Metrica | Budget mensile |
|---------|---------------|
| API unavailability | 3.6 ore (99.5%) |
| Job failure (escluso input) | 1% |
| Duplicate completions | 0 (hard limit) |

## Architettura di test

```
┌─────────────┐     ┌─────────────┐
│   Server    │────▶│   SQLite    │ (volume condiviso)
│   :8080     │     │   WAL mode  │
└──────┬──────┘     └─────────────┘
       │
       │ enqueue job
       ▼
┌─────────────┐     ┌─────────────┐
│  Worker 1-N │────▶│   Qdrant    │
│  FFmpeg     │     │   :6333     │
│  yt-dlp     │     └─────────────┘
│  Ollama     │
└─────────────┘
```

## Limiti di concurrency dal codebase

```go
// Config.Concurrency
MaxConcurrentVideoExtracts  int  // default: 1
MaxConcurrentOllamaCalls    int  // default: 1
MaxConcurrentScriptGenerations int
MaxConcurrentChannelChecks  int
MaxConcurrentIndexing       int
```

## Prossimi passi

- [ ] Allestire staging isolato con Docker Compose
- [ ] Popolare database con dati di test anonimizzati
- [ ] Verificare che tutte le dipendenze siano raggiungibili
- [ ] Validare SLO con test reali
