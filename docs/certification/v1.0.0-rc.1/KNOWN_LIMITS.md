# PR7 — Known Limits v1.0.0-rc.1

## `/ready` endpoint

### 1. Nuova connessione SQLite per ogni probe call
**Gravità**: Media.  
**Descrizione**: Il handler `Ready()` apre una nuova connessione SQLite (`sql.Open` + `db.PingContext`) ad ogni chiamata, creando un connection pool temporaneo. Sotto probe frequenti (K8s liveness ogni 10s) si accumulano handle `sql.DB` fino al GC.  
**Fix**: Iniettare un `*sql.DB` esistente nel costruttore di `HealthHandler` invece di aprirne uno nuovo ad ogni chiamata.  
**PR**: Da aprire dopo staging verification.

### 2. Readiness non verifica disponibilità job enqueue
**Gravità**: Bassa.  
**Descrizione**: La spec PR7 Fase 5 richiede che `/ready` verifichi "job enqueue disponibile". Il check attuale verifica solo DB e config. Un job broker non funzionante non viene rilevato.  
**Fix**: Aggiungere un ping al job broker o una query di integrità sulla coda job nel check di readiness.  
**PR**: Da aprire dopo staging verification.

### 3. `/ready` non distingue server da worker
**Gravità**: Bassa.  
**Descrizione**: Il worker dovrebbe verificare FFmpeg, yt-dlp e data directory scrivibile. Il check attuale è solo per il server.  
**Fix**: Aggiungere un handler readiness separato per il worker, o parametrizzare i check in base al ruolo.  
**PR**: Worker readiness — Wave futura.

## Tool di sicurezza

### 4. `gitleaks`, `govulncheck`, `golangci-lint` non installati
**Gravità**: Media.  
**Descrizione**: Non eseguibili localmente. Da installare e configurare nel CI.  
**Fix**: Aggiungere al Dockerfile di CI o installare localmente.

## `archcheck --strict`

### 5. Strict mode non passa (violazioni pre-esistenti)
**Gravità**: Alta (per PR7 exit gate).  
**Descrizione**: `go run ./scripts/archcheck --strict` esce con codice 1. Violazioni: `application_to_api` (3), `os_getenv_outside_config_app` (5), `sqlite_outside_infrastructure_database` (11), `application_to_database_sql` (28+). Vedi PR6 per il tracker completo.  
**Fix**: Risoluzione progressiva in Wave 16.

## Staging non disponibile

### 6. E2E matrix, backup/restore, smoke test non eseguiti
**Gravità**: Bloccante per release.  
**Descrizione**: Le fasi 6-12 di PR7 richiedono un ambiente di staging con Qdrant, Google Drive, Ollama, YouTube/Artlist funzionanti.  
**Fix**: Allestire staging isolato con credenziali di test.

---

## Riepilogo

| # | Issue | Gravità | Bloccante release? |
|---|-------|---------|-------------------|
| 1 | Nuovo `sql.DB` per probe call | Media | No |
| 2 | Mancato check job enqueue in `/ready` | Bassa | No |
| 3 | Mancata readiness worker | Bassa | No |
| 4 | Tool sicurezza non installati | Media | No (CI li copre) |
| 5 | `archcheck --strict` non passa | Alta | Sì |
| 6 | Staging non disponibile | Bloccante | Sì |
