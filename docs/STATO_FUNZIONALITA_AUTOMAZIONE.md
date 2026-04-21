# Stato Funzionalita Automazione Clip

Data verifica: **2026-04-19 (UTC)**

Questo documento riassume cosa e' gia operativo nel backend rispetto al flusso:
`ricerca YouTube -> download -> processing clip -> upload Drive -> update DB -> cron`.

## Coperto Oggi

| Area | Stato | Note |
|---|---|---|
| Ricerca YouTube per keyword | `OK` | Supportata via client `youtube.Client.Search(...)`. |
| Filtro temporale ricerca (`hour/today/week/month/year`) | `OK` | Supportato nel client YouTube v2 (`SearchOptions.UploadDate`). |
| Ordinamento per views | `OK` | Supportato nel client YouTube v2 (`SearchOptions.SortBy = views`). |
| Query API top views+timeframe (`/api/youtube/v2/search`) | `OK` | Espone `max_results`, `sort_by`, `upload_date`, `duration`. |
| Download video YouTube | `OK` | Supportato con `yt-dlp` wrapper e retry. |
| Download sottotitoli / transcript | `OK` | Endpoint disponibili su `/api/youtube/v2/subtitles` e `/api/youtube/v2/transcript`. |
| Upload clip su Google Drive | `OK` | Supportato e testato in pipeline dinamica (`Dynamic clip uploaded...`). |
| Creazione sottocartelle Drive | `OK` | Supportata con `GetOrCreateFolder` durante upload. |
| Aggiornamento DB dopo nuove clip | `OK` | Presente in pipeline dinamica + sync post-cycle (`Post-cycle DB sync completed`). |
| Ricerca e download multipli in parallelo | `OK` | Harvester con worker concorrenti (`MaxConcurrentDls`). |
| Monitor canali YouTube periodico | `OK` | Channel monitor service disponibile (abilitabile da env). |
| Timeframe monitor canali configurabile | `OK` | Supporta `video_timeframe: 24h|week|month`. |
| Cron scheduler stock | `OK` | Stock scheduler presente (abilitabile da env). |
| Gestione cron harvester via API | `OK` | Route disponibili su `/api/harvester/cron/*`. |

## Coperto Parzialmente

| Area | Stato | Limite attuale |
|---|---|---|
| Download clip multiple nello stesso video monitor | `PARZIALE` | Estrae fino a 5 highlights, ma upload/download nel loop del monitor sono sequenziali. |
| Persistenza job cron harvester | `PARZIALE` | I job cron sono gestibili via API ma non vengono ancora persistiti automaticamente al riavvio. |

## Non Coperto Oggi

| Area | Stato | Note |
|---|---|---|
| Upload automatico su YouTube delle clip finali | `NO` | Esiste client upload YouTube nel codice, ma non c'e un flusso API end-to-end pronto per questo use case. |
| Pipeline "Courtroom + Boxe + immagini AI Nvidia + video AI" unica | `NO` | Componenti separati esistono, ma non una singola orchestrazione completa pronta da endpoint unico. |

## Evidenze Tecniche Principali

- Inizializzazione servizi background: `cmd/server/init_background.go`
- Monitor canali: `internal/service/channelmonitor/*`
- Scheduler stock: `internal/stockjob/scheduler.go`
- Harvester concorrente: `internal/harvester/harvester.go`
- Client YouTube v2 (sort/timeframe): `internal/youtube/client.go`, `internal/youtube/backend_ytdlp.go`
- Log test recenti con upload + sync DB: `server_test_tesla_postcycle.log`, `server_test_clipsearch_e2e_fast.log`

## Priorita Implementazione Consigliate

1. Collegare `CronManager` al router con endpoint dedicati (senza conflitti route).
2. Parallelizzare upload/download highlights nel monitor.
3. Aggiungere endpoint unico "keyword -> top views week -> clip -> Drive -> DB".
