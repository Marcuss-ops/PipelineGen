# Documentazione Velox Editing

Documentazione completa del progetto organizzata per area.

## 📁 Struttura

```
docs/
├── README.md                          # Questo file
├── scalability/                       # Scalabilità 1000+ worker
│   ├── README.md
│   └── SCALABILITY_IMPLEMENTATION.md
├── architecture/                      # Architettura sistema
│   ├── ARCHITETTURA_JOB_DISTRIBUZIONE.md
│   └── PROCESSO_CREAZIONE_VIDEO.md
└── (altri file .md vari)
```

## 📚 Aree Documentate

### Scalabilità
Sistema per gestire 1000+ worker simultanei. Vedi [scalability/](./scalability/README.md).

### Architettura
Documentazione architettura sistema:
- Distribuzione job
- Processo creazione video
- Design decisions

Vedi [architecture/](./architecture/) per dettagli.

## 🔍 File Markdown Vari

Altri file `.md` nella root di `docs/`:
- **[REMOTE_ENDPOINTS.md](./REMOTE_ENDPOINTS.md)** – Endpoint Master/Worker, API script/voiceover/Docs, **docs pubbliche** (`GET /api/docs`), client-config, come usare da browser e curl
- **[API_TRIGGER_QUESTO_PC.md](./API_TRIGGER_QUESTO_PC.md)** – Accesso API e documentazione (server spento/acceso), **accensione API da remoto** (wake server su porta 4999: `/wake`, `/status`)
- **[FRONTEND_API_CHIAMATE.md](./FRONTEND_API_CHIAMATE.md)** – Interazione frontend–API, disponibilità docs, wake da remoto, elenco chiamate per flusso
- **[API_CONTRATTO_BACKEND.md](./API_CONTRATTO_BACKEND.md)** – Contratto esatto: endpoint, body e risposta per ogni chiamata (per adattare solo il backend)
- Documentazione vari aspetti del sistema
- Note tecniche
- Guide operative
