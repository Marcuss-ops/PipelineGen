# PR8 — Capacity Plan v1.0.0-rc.1

## Capacity Model

### Worker capacity formula

```
worker_capacity_jobs_per_hour = 3600 / avg_job_duration_seconds
effective_capacity = worker_capacity_jobs_per_hour * worker_count * efficiency_factor

where:
  efficiency_factor = 0.7 (contention, provider rate-limit, lease overhead)
```

### Worker count formula

```
workers_needed = ceil(peak_job_arrival_per_hour / effective_capacity_per_worker)
```

### Storage projection

```
storage_monthly_gb = avg_clip_size_gb * clips_per_month * retention_days / 365
db_growth_monthly_mb = avg_rows_per_job * jobs_per_month * avg_row_size_kb / 1024
```

## Concrete estimates (da validare)

| Parametro | Stima |
|-----------|-------|
| avg_job_duration (extraction) | 300s (5 min) |
| avg_job_duration (script) | 600s (10 min) |
| avg_job_duration (lightweight) | 60s |
| avg_clip_size | 50MB |
| clips_per_month | 1500 |
| retention_days | 90 |
| storage_monthly | ~18GB locale + Drive |
| db_growth_monthly | ~150MB |
| jobs_per_month | ~500 |

### Saturation points (CPU-only, stimati)

| Risorsa | Punto di saturazione | Sintomo |
|---------|---------------------|---------|
| CPU worker | 100% su tutti i core | Code job, p95 latency ↑ |
| SQLite | >4 worker concorrenti | `SQLITE_BUSY`, claim latency ↑ |
| Disco I/O | FFmpeg + yt-dlp simultanei | Throughput extraction ↓ |
| Drive API | 429 responses | Upload backlog |
| Ollama | 1 chiamata alla volta (default) | Enrichment serializzato |
| yt-dlp | Rate-limit YouTube | Extraction ritentato |

### Headroom minimo

```
CPU headroom: 20% sui worker (per burst)
Disco headroom: 30% libero (per temp file)
DB WAL headroom: checkpoint ogni 1000 pagine
Worker headroom: +1 oltre il calcolato
```

## Scaling policy

### Scale out quando:

- Queue depth > 5 job per > 5 minuti
- Oldest job age > 2× SLO
- CPU media worker > 80% per > 10 minuti
- p95 start latency > 30s per > 5 minuti

### NON scalare quando:

- Provider è rate-limited (YouTube 429, Drive 429)
- SQLite è già il collo di bottiglia (>4 worker)
- Disco è saturo (>80% IO)
- Error rate indica bug, non capacità

### Scale in:

- Drain worker: nessun nuovo job claim
- Attendere job attivi completino (max 15 min timeout)
- Rimuovere worker
- Minimo 1 worker garantito

## Cost Model (VM CPU-only, stima)

| Configurazione | Worker | vCPU | RAM | Costo/mese (stimato) |
|---------------|--------|------|-----|---------------------|
| 1× target | 1 | 4 | 8GB | ~$50-100 |
| 2× target | 2 | 8 | 16GB | ~$100-200 |
| 4× target | 4 | 16 | 32GB | ~$200-400 |

## Prossimi passi

- [ ] Validare le formule con dati reali di staging
- [ ] Misurare avg_job_duration per ogni job type
- [ ] Testare saturation point con load generator
- [ ] Confermare costo con provider cloud
