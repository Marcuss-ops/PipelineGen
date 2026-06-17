# Endpoint: Script Generation & Smart Image Routing
**Path**: `POST /api/script/generate-from-clips`
*(Alias retrocompatibile: `POST /api/script/generate-with-images`)*

Questo è l'**endpoint principale e unificato** del sistema PipelineGen per la creazione di script narrativi. È in grado di operare in due modalità:
1. **Clip-Aware (Video)**: Fornendo una lista di `clip_ids`, genera una narrazione coerente basata sul contenuto visivo e semantico dei video.
2. **Text-to-Image (Generazione da Zero)**: Se invocato con un `topic` testuale e parametri di stile (es. `style: "cinematic"`), genera lo script da zero e attiva la **Smart Generation** per creare automaticamente le immagini a corredo di ogni scena.

---

## 🌊 Il Flusso di Generazione Immagini (Smart Generation)

Quando l'endpoint viene interrogato per generare uno script testuale completo di immagini, segue questo flusso ottimizzato:

### 1. Generazione Testuale e Segmentazione
L'LLM (tramite Gemma/Ollama o VLM) genera l'intero script in base a `topic`, `tone` e `language`. Lo script viene poi segmentato in logiche **scene narrative** (di default, 2 o 3 frasi per scena, regolabile tramite `sentences_per_image`).

### 2. Generazione Immagini Parallela (Google Slides/Vids)
Per ogni scena, viene avviata una richiesta di generazione immagine in parallelo.
- L'engine Go sfrutta il microservizio Python `google-accounting`.
- Il microservizio Python gestisce un **Pool di Sessioni Pre-Riscaldate** (`session_pool.py`), assegnando a ogni job uno "slot" isolato. 
- In modalità isolata, ogni slot lavora su un **progetto Google Vids/Slides dedicato e univoco** per evitare collisioni UI durante i click automatizzati (gestiti da Playwright).

### 3. Fallback Resiliente (NVIDIA NIM)
Se Google Vids dovesse fallire (es. timeout o blocco UI), il sistema Go cattura l'errore e passa automaticamente e istantaneamente alla generazione dell'immagine tramite modelli locali/cloud NVIDIA (es. `flux.1-schnell` o `local-nim`).

### 4. Routing su Google Drive basato sullo Stile
Una volta scaricata l'immagine generata (catturata analizzando il traffico di rete `DOM/NET` per la massima qualità), il backend Go interviene per il salvataggio:
- Analizza lo `style` richiesto (es. `cinematic`, `medieval`, `cyberpunk`).
- Utilizza la mappatura interna (`aiImageDriveRootForSource`) per **identificare l'ID della cartella Google Drive specifica** per quello stile (es. *cinematic* -> `1t6bhe8kqu...`).
- All'interno di questa root, crea una **sottocartella univoca basata sul prompt** (slug-text + hash).
- Esegue l'upload di due file in questa sottocartella:
  1. L'immagine generata (`.jpg` o `.png`).
  2. Il `metadata.json` completo di prompt, tag generati dall'LLM ed embedding semantici.

### 5. Indicizzazione Interna
Al termine dell'upload su Drive, l'immagine viene registrata localmente nel database SQLite (`media_assets`) per ricerche full-text e inserita nel Vector Store (Qdrant) per consentire ricerche semantiche o riutilizzo futuro tramite Cache.

---

## 📦 Formato della Risposta (Output JSON)

La risposta API al termine del job restituisce un payload completo che include l'identificativo del Google Doc generato e l'array dettagliato di tutte le scene.

Ogni scena espone il testo narrativo e i link **diretti di Google Drive** alle immagini appena generate, pronte per essere utilizzate nei tool di video editing.

### Esempio di Risposta di Successo:

```json
{
  "ok": true,
  "job_id": "job_1781553371481964902_e6bf0d71",
  "progress": 100,
  "status": "completed",
  "result": {
    "cache_status": "generated",
    "doc_id": "1Y56ImBAYqKAIfv-dBpthdG15AjkJ1QsEoVbsaX9MbjM",
    "doc_url": "https://docs.google.com/document/d/1Y56ImBAYqKAIfv-dBpthdG15AjkJ1QsEoVbsaX9MbjM/edit?usp=drivesdk",
    "language": "en",
    "ok": true,
    "title": "The secret life of bioluminescent fungi in deep caves and underwater tunnels",
    "word_count": 272,
    "timings": {
      "total_ms": 156000
    },
    "script": "Deep beneath the weight of the world, where sunlight has never dared to penetrate, lie subterranean cathedrals: labyrinthine deep caves and submerged tunnels... (intero testo combinato)",
    "scenes": [
      {
        "text": "Deep beneath the weight of the world, where sunlight has never dared to penetrate, lie subterranean cathedrals: labyrinthine deep caves and submerged tunnels. These worlds operate by rules entirely alien to surface life, communicating through a silent, spectral language spoken only in light. Here, suspended in profound blackness, we find bioluminescent fungi—living miracles that are not geological formations, but delicate networks of mycelium performing a biological ballet.",
        "image": "https://drive.google.com/file/d/1q8jBuHRMmrxunqs99PFvWDaDulzK2Aiz/view",
        "images": [
          "https://drive.google.com/file/d/1q8jBuHRMmrxunqs99PFvWDaDulzK2Aiz/view"
        ]
      },
      {
        "text": "The glow is nothing more than a chemical whisper: the reaction between oxygen and specialized organic compounds involving luciferin and the enzyme luciferase, releasing photons in a cold, gentle illumination. These fungi are masters of adaptation in nutrient-poor environments. Their soft radiance is no decoration; it is an ancient survival mechanism.",
        "image": "https://drive.google.com/file/d/1eWYGqt_6j71qyo3pWI4ZpAkHlRwuOyDW/view",
        "images": [
          "https://drive.google.com/file/d/1eWYGqt_6j71qyo3pWI4ZpAkHlRwuOyDW/view"
        ]
      },
      {
        "text": "This subtle greenish-blue luminescence acts as an irresistible beacon, drawing in specialized beetles, blind flies, and small insects navigating these desolate passages. The light manipulates the world around them: when a creature lands upon a cap, drawn by the glow, it unknowingly becomes an essential courier, carrying fungal spores to distant locations, ensuring life’s continuation where nothing else can root. The mystery deepens in underwater tunnels, where immense hydrostatic pressure reigns and light is utterly extinguished.",
        "image": "https://drive.google.com/file/d/1iwh_m8ULLyqfyH-x7awfC5_dXOxewHGW/view",
        "images": [
          "https://drive.google.com/file/d/1iwh_m8ULLyqfyH-x7awfC5_dXOxewHGW/view"
        ]
      },
      {
        "text": "These fungi maintain their silent vigil, representing evolution's defiant ability to glow even amidst absolute void. To study them is to peer into the deepest secrets of biochemistry. They serve as profound reminders that the most extraordinary wonders are often hidden—not in grand vistas, but in the quiet dampness beneath our feet.",
        "image": "https://drive.google.com/file/d/1eGoBqxnlReE0MM4qVhd1WGK6BUiYmeQO/view",
        "images": [
          "https://drive.google.com/file/d/1eGoBqxnlReE0MM4qVhd1WGK6BUiYmeQO/view"
        ]
      },
      {
        "text": "These bioluminescent fungi remain nature’s most mysterious glow, guiding life through the darkest corners of the earth across eons of silent darkness.",
        "image": "https://drive.google.com/file/d/1g-eO3EjoShDbIH-pBMA-LcxafW3dz2Kc/view",
        "images": [
          "https://drive.google.com/file/d/1g-eO3EjoShDbIH-pBMA-LcxafW3dz2Kc/view"
        ]
      }
    ]
  }
}
```
