# 🚀 VeloxEditing Project Guide

Benvenuto nel progetto **VeloxEditing**! Questa guida ti aiuterà a capire come funziona il sistema, come configurarlo e come utilizzare i suoi endpoint principali, specialmente per la generazione di script (Ollama) e la gestione dei contenuti (Artlist).

---

## 📖 Cos'è VeloxEditing?

VeloxEditing è un sistema automatizzato per la creazione di contenuti video. Si occupa di:
1.  **Scrittura Script:** Utilizza modelli AI locali (tramite Ollama) per generare narrazioni coerenti.
2.  **Asset Matching:** Analizza lo script per trovare video di stock (da Google Drive o database locali).
3.  **Content Harvesting:** Scarica automaticamente nuovi video da fonti come Artlist.
4.  **Pubblicazione:** Carica i documenti generati su Google Docs per la revisione finale.

---

## 🛠️ Requisiti Iniziali

Prima di iniziare, assicurati di avere:
- **Go 1.25+** installato.
- **Ollama** in esecuzione con il modello `gemma3:4b` (o simile).
- **yt-dlp** (per il download dei video).
- Un database **SQLite** (creato automaticamente al primo avvio in `data/`).

---

## 🚀 Guida Rapida per Iniziare

1.  **Installa Ollama:** Scaricalo da [ollama.com](https://ollama.com).
2.  **Scarica il modello:** `ollama run gemma3:4b`.
3.  **Configura il progetto:** Modifica `config.yaml` se necessario (l'URL di default è `http://localhost:11434`).
4.  **Avvia il server:**
    ```bash
    cd src/go-master
    go run cmd/server/main.go
    ```
    Il server sarà disponibile su `http://localhost:8080`.

---

## 🧠 Utilizzo di Ollama (Script Generation)

L'integrazione con Ollama permette di generare script video partendo da un semplice argomento.

### Endpoint: `POST /api/script-docs/generate`

Questo endpoint genera un documento completo con:
- Titolo e Metadati.
- Narrazione (Script).
- Timeline (Suggerimenti di scene).
- Analisi delle entità e suggerimenti per Artlist.

**Esempio di richiesta (CURL):**
```bash
curl -X POST http://localhost:8080/api/script-docs/generate \
     -H "Content-Type: application/json" \
     -d '{
       "topic": "La storia del caffè espresso",
       "duration": 60,
       "language": "it",
       "template": "documentary"
     }'
```

**Come funziona tecnicamente:**
1.  Il server invia una serie di messaggi a Ollama (System Prompt + User Request).
2.  Ollama genera lo script.
3.  Il sistema analizza lo script per identificare parole chiave e nomi propri (Entità).
4.  Viene generata una Timeline che suddivide il video in segmenti temporali.
5.  Il risultato viene salvato localmente in `data/scripts/` e caricato su Google Docs.

---

## 🎬 Utilizzo di Artlist & Harvester

Il modulo Artlist serve a popolare il tuo database di video di stock.

### Endpoint: `POST /api/artlist/run`

Questo avvia una "Pipeline" che cerca video su Artlist, li scarica e li organizza.

**Esempio di richiesta (CURL):**
```bash
curl -X POST http://localhost:8080/api/artlist/run \
     -H "Content-Type: application/json" \
     -d '{
       "tag": "nature",
       "limit": 5,
       "auto_download": true,
       "auto_upload": true
     }'
```

**Flusso della Pipeline:**
1.  **Search:** Cerca video che corrispondono al tag specificato.
2.  **Download:** Scarica i file video utilizzando il `downloader` interno.
3.  **Metadata:** Estrae informazioni dai video.
4.  **Upload:** (Opzionale) Carica i file su Google Drive.
5.  **Database:** Registra i video in `data/velox.db.sqlite` per poterli usare nel matching degli script.

---

## 🏗️ Architettura in Dettaglio

### 1. Go Master (`src/go-master`)
È l'orchestratore. Gestisce le API, il database e coordina i vari servizi.
- **internal/matching:** Il cuore logico che decide quale video usare per ogni frase dello script.
- **internal/ml/ollama:** Il client che parla con l'AI locale.

### 2. Harvester & Downloader
Servizi (spesso eseguiti tramite cron o via API) che si occupano di recuperare contenuti dal web.

### 3. Database (SQLite)
Usiamo tre database principali in `data/`:
- `velox.db.sqlite`: Il database principale (script, job, configurazioni).
- `stock.db.sqlite`: Indice dei video disponibili.
- `images.db.sqlite`: Indice delle immagini.

---

## 🔍 Il Sistema di Matching (Come vengono scelti i video?)

Quando generi uno script, il sistema non sceglie i video a caso. Ecco il processo:
1.  **Normalizzazione:** Lo script viene pulito (rimozione punteggiatura, minuscole).
2.  **Tokenizzazione:** Le frasi vengono divise in parole chiave.
3.  **Scoring:** Il sistema confronta le parole chiave dello script con i tag dei video nel database.
4.  **Decisione:** Viene selezionato il video con il punteggio più alto che non è stato già usato troppo spesso.

---

## 🛠️ Risoluzione Problemi comuni

- **Ollama non risponde:** Assicurati che Ollama sia attivo (`ollama list`) e che il modello configurato esista.
- **Porta 8080 occupata:** Se ricevi `address already in use`, un altro server è già attivo. Usa `pkill -f server` per chiuderlo.
- **Video non trovati:** Se la timeline non ha video associati, assicurati di aver eseguito almeno una `artlist/run` per popolare il database.

---

*Guida aggiornata al 30 Aprile 2026 — VeloxEditing Backend*
