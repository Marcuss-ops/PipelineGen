# TTS locale open-weight — pilota inglese (2026-11-09)

Stato: **Pocket TTS eseguibile e misurato**; Phonon 31M **non eseguibile su
questo host** (SIGILL del runtime `ptts`). Dei modelli open-weight più grandi,
solo **CosyVoice 3 0.5B** è utilizzabile (RTF 2,65 su GPU); Qwen3 1.7B e
Audio8 0.6B sono stati misurati e **scartati** per costo (RTF ~8,4), Fish Drama 3
non entra nella VRAM disponibile.

Edge TTS resta la baseline per tutte le 10 lingue. Il pilota locale è
**opt-in e limitato all'inglese**: nessuna lingua esistente cambia provider
finché `voiceover.local_tts.enabled` è `false` (default).

## 1. Cosa è stato implementato

| Componente | File |
|---|---|
| Sidecar HTTP persistente | `scripts/bridges/tts_local_server.py`, `scripts/bridges/local_tts_bridge/{server,request,engines}.py` |
| Contratto wire | identico a Edge: `POST /synthesize`, `GET /health`, `GET /engines`, `POST /quit` |
| Adapter Go | `internal/platform/audio/processor.go` con bridge/script/providerID configurabili |
| Router lingua | `internal/app/wiring/voiceover/local_tts_router.go` (solo `en` verso locale) |
| Config | `internal/platform/config/types.go` → `voiceover.local_tts` |
| Cache | `BuildVoiceoverContentFingerprintV3` + provider identity nell'idempotency key |
| Harness | `ops/jobs/tts_local_pilot_20261109/bench.py` |

Il sidecar non importa nessun motore all'avvio: un motore rotto non impedisce
agli altri di partire o di dichiararsi disponibili.

## 2. Motori e licenze

| Nome wire | Modello | Licenza | Stato su questo host |
|---|---|---|---|
| `pocket` | Kyutai Pocket TTS 4.0.1 (main ~100M; 18M/7M/2M) | codice MIT; pesi CC-BY-4.0; condizioni delle voci da verificare | **funzionante** |
| `phonon` | Gradium Phonon 31M (`phonon-31m-en`) | Apache-2.0 | **SIGILL** (`import ptts`, exit 132 / -4) su Ivy Bridge |
| `qwen3` | Qwen3-TTS 1.7B (`Qwen3-TTS-12Hz-1.7B-CustomVoice`, voce Ryan) | Apache-2.0 | **misurato, escluso**: RTF 8,38 su GPU |
| `cosyvoice` | CosyVoice 3 0.5B (`Fun-CosyVoice3-0.5B`) | Apache-2.0 | **installato e misurato**: RTF 2,65, 24 kHz |
| `fish` | Fish Audio Drama 3 (35B-A3B) | licenza condizionata: verificare prima | **non eseguibile qui**: ≥48 GB in FP8 vs 16 GB della A4000 |
| `audio8` | Audio8 Realistic Air 0.6B | Apache-2.0 (famiglia 0.6B) | **misurato, escluso**: RTF 8,43 su GPU |

Non è stato introdotto nessun provider a pagamento (Azure, ElevenLabs,
Cartesia, Polly, Google, Deepgram): erano già fuori dalla rosa richiesta.

### Perché Phonon non parte

`python3 -c "import ptts"` termina con **Illegal instruction (core dumped)**.
La CPU è una Intel Xeon E3 v2 (Ivy Bridge), senza AVX2. Il wheel `ptts`
0.7.0 richiede un'ISA superiore. L'adapter esegue il probe in un
**sottoprocesso**: il sidecar resta vivo e `/engines` riporta il motivo reale
invece di crashare. Su hardware supportato basta:

```bash
export LOCAL_TTS_CMD_PHONON="ptts ... {text} ... {out}"
```

Il comando deve scrivere un WAV (o audio leggibile da ffmpeg) in `{out}`.
Placeholder disponibili: `{text}`, `{out}`, `{voice}`, `{lang}`.

## 3. Misure reali — Pocket TTS

Host: Xeon E3-12xx v2 (Ivy Bridge), CPU-only. Modello `english` 4.0.1,
voce `alba`, 3 frasi inglesi reali.

| # | Testo | Sintesi | Audio | RTF |
|---|---|---|---|---|
| 1 | "In the spring of 1987, Maria moved to Bristol..." | 38.052 s | 5.280 s | 7.21 (include load modello) |
| 2 | "The city council approved a budget of 4.2 million pounds..." | 5.180 s | 5.520 s | **0.94** |
| 3 | "Wait — you're telling me the bridge was never finished?" | 2.795 s | 2.880 s | **0.97** |

- Dopo il primo giro il costo è **circa 1× la durata dell'audio** (RTF ~0,95).
- Il primo giro pagava ~33–35 s di caricamento del modello.
- Per 1.000 parole (~6–7 minuti di parlato) servono quindi ~6–7 minuti di CPU,
  contro ~1–1,3 s per scena di Edge (misura già presente nel repo).
- Non è una sostituzione a costo zero: è un motore offline, ~4–5× più lento
  per scena corta, ma senza dipendenza dal servizio Microsoft.

Questi numeri sono di **throughput**, non di qualità percepita: il giudizio
sulla voce richiede l'ascolto dei sample.

### Sample di ascolto (1920×1080, H.264, ≤10 s)

Cartella Drive: [tts_local_pilot_20261109](https://drive.google.com/drive/folders/10AcFzMIqU-89uDh27UDcc6vsRlfPLBw7)
(dentro la cartella condivisa `1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS`).

| Motore | Voce | Durata | Link |
|---|---|---|---|
| Pocket TTS | alba | 5,84 s | [apri](https://drive.google.com/file/d/19CZmA0hmje1gWOKxsn_rS-k8ks93YQK3/view?usp=drivesdk) |
| Pocket TTS | anna | 6,57 s | [apri](https://drive.google.com/file/d/18uZ4avMGlqfXY8TfqVJCtunkmT44TAv5/view?usp=drivesdk) |
| Pocket TTS | jean | 5,60 s | [apri](https://drive.google.com/file/d/1uYMTuPIVbW0MBSRMLF4crrDc9Kl9GLc2/view?usp=drivesdk) |
| Edge TTS (baseline) | en-US-ChristopherNeural | 6,73 s | [apri](https://drive.google.com/file/d/1aMcTXF-d_7E3Ftxd6YUEji0iqOcXguuC/view?usp=drivesdk) |

Stessa frase per tutti e quattro: ascolto in cieco possibile senza il
rendering video completo. Manifest locale con SHA-256:
`ops/jobs/tts_local_pilot_20261109/samples/upload_manifest.json`.

### 50 frasi lunghe, 10 voci maschili (2026-11-09)

Corpus: `ops/jobs/tts_local_pilot_20261109/sentences_50_long.en.txt`
(50 frasi da 20–40 parole, ~1.200 parole). 6 voci Pocket maschili + 4 voci
Edge maschili, 50 frasi per voce, **0 errori** su 300 sintesi Pocket e 200
Edge.

| Motore | Voce | Audio 50 frasi | Sintesi | RTF | Mediana sintesi |
|---|---|---:|---:|---:|---:|
| Pocket | jean | 344,08 s | 770,06 s | 2,238 | 14,2 s |
| Pocket | paul | 397,36 s | 853,85 s | 2,149 | 16,3 s |
| Pocket | michael | 379,68 s | 847,57 s | 2,232 | 16,6 s |
| Pocket | charles | 387,84 s | 838,48 s | 2,162 | 16,3 s |
| Pocket | george | 352,48 s | 810,37 s | 2,299 | 15,2 s |
| Pocket | stuart_bell | 439,28 s | 902,28 s | 2,054 | 18,1 s |
| Edge | en-US-ChristopherNeural | 425,40 s | 154,10 s | 0,362 | 2,9 s |
| Edge | en-US-EricNeural | 432,38 s | 166,43 s | 0,385 | 3,4 s |
| Edge | en-CA-LiamNeural | 388,15 s | 90,65 s | 0,234 | 1,7 s |
| Edge | en-GB-RyanNeural | 411,91 s | 116,35 s | 0,283 | 2,3 s |

Totale Pocket: 2.300,7 s di audio, 5.022,6 s di sintesi, **RTF 2,18**.
Totale Edge: 1.657,9 s di audio, 527,5 s di sintesi, **RTF 0,32**.

**Attenzione alla concorrenza**: le 6 voci Pocket sono state eseguite in 6
processi paralleli. Il RTF 2,05–2,30 è quello **con contesa di CPU**; un test
isolato su 3 frasi lunghe dà RTF 1,07–1,22. Per la produzione usare
`max_concurrent: 1` (al massimo 2), non 6: il fan-out non aumenta il
throughput aggregato di Pocket.

Sample maschili (stessa frase, 1920×1080, ≤10 s):
[cartella Drive](https://drive.google.com/drive/folders/1aIGSCowwJgWVTJgmeny7Geu0quA1WfBy).

| Motore | Voce | Durata | Link |
|---|---|---|---|
| Pocket | jean | 8,20 s | [apri](https://drive.google.com/file/d/1sKYVHVKhyi3N5edAE855lDAIS50jMYmP/view?usp=drivesdk) |
| Pocket | paul | 9,24 s | [apri](https://drive.google.com/file/d/1ObJ7aM9D_EzCVn1ycHKcUDAcD1tUTfdh/view?usp=drivesdk) |
| Pocket | michael | 9,00 s | [apri](https://drive.google.com/file/d/1EtHuswKndu1SAg0HVztQRq7GL7zag7aT/view?usp=drivesdk) |
| Pocket | charles | 9,08 s | [apri](https://drive.google.com/file/d/1bUHTpAzQhK2QRm4jiFTJeVwTRMuAy4-m/view?usp=drivesdk) |
| Pocket | george | 8,37 s | [apri](https://drive.google.com/file/d/1A0tyDBjk-tPSWqrod9p04T0xaNv6Ax8C/view?usp=drivesdk) |
| Pocket | stuart_bell | 10,00 s | [apri](https://drive.google.com/file/d/1rtzIJk4CHcxIcg3BtgNvgQCrVSnmJW-9/view?usp=drivesdk) |
| Edge | en-US-ChristopherNeural | 9,77 s | [apri](https://drive.google.com/file/d/1jqAk3ZqnuOUXRGhicG3YOiHVM5TAa0Ta/view?usp=drivesdk) |
| Edge | en-US-EricNeural | 9,86 s | [apri](https://drive.google.com/file/d/1TmdrIfYMfgpANa7M9hyoA597bVMD-Hkd/view?usp=drivesdk) |
| Edge | en-CA-LiamNeural | 8,83 s | [apri](https://drive.google.com/file/d/1S8fvxteRKMICJFtrE7xDLa2sgu4-CnbJ/view?usp=drivesdk) |
| Edge | en-GB-RyanNeural | 9,45 s | [apri](https://drive.google.com/file/d/1aj5O_AHaa8RfwT1h1TxUceyazE0R-VO_/view?usp=drivesdk) |

### Modelli open-weight pesanti — confronto su GPU (2026-11-09)

Hardware: RTX A4000 16 GB (condivisa con altri servizi), un modello alla volta.
Corpus: stesse frasi lunghe. Il criterio che conta è il costo per frase: se un
motore impiega oltre un minuto per una frase, su un voiceover da 1.000 parole
(~70 frasi) diventa un'ora di GPU per 6 minuti di audio.

| Motore | Voce | Frasi | Audio | Sintesi | RTF | Mediana per frase |
|---|---|---:|---:|---:|---:|---:|
| Qwen3-TTS 1.7B CustomVoice | Ryan (preset, maschile) | 37 | 360,56 s | 3.023,04 s | **8,38** | 77,4 s |
| Audio8 Realistic Air 0.6B | clone zero-shot | 10 | 91,07 s | 767,44 s | **8,43** | 74,4 s |
| CosyVoice 3 0.5B | clone zero-shot | 10 | 81,52 s | 215,75 s | **2,65** | 20,0 s |
| *Pocket TTS (riferimento, CPU)* | *alba* | *50* | *344,08 s* | *770,06 s* | *2,24* | *14,2 s* |
| *Edge TTS (baseline)* | *en-US-ChristopherNeural* | *50* | *425,40 s* | *154,10 s* | *0,36* | *2,9 s* |

Zero errori in tutti i run. Qwen3 è stato **fermato a 37/50**: il RTF 8,38 era
già definitivo e altre 13 frasi avrebbero solo consumato GPU. I risultati
parziali sono ricostruiti da `out/qwen/results.json` (`salvage_qwen.py`).

**Conclusione operativa**:

- **Qwen3 1.7B e Audio8 0.6B: esclusi.** RTF ~8,4 significa ~75 s di GPU per
  una frase da 10 s; per un video di 10 minuti servono ~1,5 ore. Non è un
  problema di VRAM (Qwen3 occupa 4,2 GB in bf16): è il decoding autoregressivo
  seriale, con la GPU al 4–17% di utilizzo.
- **CosyVoice 3 0.5B: unico utilizzabile.** RTF 2,65 su GPU, 24 kHz, e soprattutto
  **clona la voce da 10 s di riferimento** (zero-shot) invece di offrire un
  catalogo di preset. È però appena più lento di Pocket su CPU (RTF 2,24): non
  sostituisce Pocket sul throughput, aggiunge la possibilità di una voce
  maschile arbitraria. Con un riferimento maschile regge le 50 frasi intere a
  RTF 2,67 (vedi sotto).
- **Fish Drama 3: non eseguibile.** 35B-A3B, richiede ≥48 GB in FP8; 16 GB non
  bastano nemmeno con offload parziale.

Il contratto word-boundary non cambia: nessuno di questi motori espone
boundary nativi, quindi restano su policy `best_effort` come Pocket.

Sample di ascolto (stessa frase per entrambi, 1920×1080, ≤10 s) — cartella
Drive `tts_models_20261109_light` dentro
`1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS`:

| Motore | Voce | Audio | Video | Link |
|---|---|---:|---:|---|
| CosyVoice 3 0.5B | clone (rif. Pocket alba) | 7,80 s | 8,80 s | [apri](https://drive.google.com/file/d/1xUjD6jet45TWSZYQLRhGeXPGe9QeZmNT/view?usp=drivesdk) |
| Audio8 Realistic Air 0.6B | clone (rif. Pocket alba) | 9,15 s | 10,00 s | [apri](https://drive.google.com/file/d/1ZMc0S3Nb-YxNqOUwlRQnfIFCco90ZO2M/view?usp=drivesdk) |

Manifest con SHA-256: `ops/jobs/tts_models_20261109/samples_light/upload_manifest.json`.

### Voci maschili — CosyVoice 3 in clone su 50 frasi (2026-11-09)

Il primo run CosyVoice usava `pocket_alba.wav` come riferimento, quindi una voce
femminile. Per il voiceover maschile il riferimento va costruito: Pocket `jean`
(8,08 s) e `paul` (9,76 s) sintetizzano in CPU una frase del corpus, l'audio
viene passato a CosyVoice come prompt zero-shot con la trascrizione esatta.

| Motore | Voce | Frasi | Audio | Sintesi | RTF | Mediana | Load |
|---|---|---:|---:|---:|---:|---:|---:|
| CosyVoice 3 0.5B | clone di Pocket jean | 50 | 384,76 s | 1.033,68 s | 2,687 | 20,1 s | 74,9 s |
| CosyVoice 3 0.5B | clone di Pocket paul | 50 | 426,60 s | 1.140,66 s | 2,674 | 22,3 s | 94,5 s |

Zero errori su 100 frasi. Il clone maschile costa **quanto** quello femminile
(2,65 → 2,67): il sesso del riferimento non cambia il costo, cambia il timbro.
Pocket resta più economico sulla stessa voce (jean: RTF 2,24 con sei processi
in parallelo, ~1,1–1,2 su un processo isolato).

Trappola: CosyVoice stampa `synthesis text ... too short than prompt text`
quando il target è più corto del prompt e la qualità cala. Il primo riferimento
jean era 16,2 s (tre frasi) e l'avviso compariva su metà corpus; rifatto a 8,08 s
(una frase) l'avviso sparisce. Per questo `make_male_refs.py` ha
`TARGET_SECONDS = 8`.

Sample (frase 44, 1920×1080, ≤10 s) — cartella Drive `tts_models_20261109_male`:

| Motore | Voce | Audio | Video | Link |
|---|---|---:|---:|---|
| Pocket TTS | jean (nativa) | 8,40 s | 9,40 s | [apri](https://drive.google.com/file/d/1_RTVZO04Ln-mZn9VGy3vg9ab1hKETXK8/view?usp=drivesdk) |
| CosyVoice 3 | clone di jean | 8,08 s | 9,08 s | [apri](https://drive.google.com/file/d/1trxAU5ryJ3FbBZIKW_WSI_Uwuwv_bDSM/view?usp=drivesdk) |
| Pocket TTS | paul (nativa) | 8,80 s | 9,80 s | [apri](https://drive.google.com/file/d/1_WI9OgrdI6wq71mlBjbQ4yJTG-o45r-1/view?usp=drivesdk) |
| CosyVoice 3 | clone di paul | 8,96 s | 9,97 s | [apri](https://drive.google.com/file/d/1BGiYsFS0Cv7sS5vxyoXYP9EKzL5L7Ka_/view?usp=drivesdk) |

Manifest: `ops/jobs/tts_models_20261109/samples_male_models/upload_manifest.json`.
Le sei voci Pocket maschili sulle 50 frasi sono già misurate (RTF 2,05–2,30).
Il catalogo `pocket-tts` 4.0.1 ha **27 voci predefinite e nessun metadato di
sesso**: le sei usate sono quelle classificate maschili nel pilota. Restano
fuori `bill_boerst` e `peter_yearsley` (donor Voice-Zero, probabili maschili) e
le voci maschili non inglesi (`giovanni`, `juergen`, `rafael`, `daan`).

**Set canonico ordinato (2026-11-10)** — solo i due motori leggeri, Edge
escluso: cartella
[`male_pocket_cosy_20261110`](https://drive.google.com/drive/u/1/folders/1TdP46RsYpT6V6hyUL5YQesp2UagIklXe)
con [`pocket/`](https://drive.google.com/drive/u/1/folders/1hJEjQm4oAjCtJ1eN7n67xy4d9wmv4w62)
(sei voci native) e [`cosy/`](https://drive.google.com/drive/u/1/folders/1yt662cZZg7mqEyqpd9tRpyK-aL61PjPe)
(clone di jean e paul), stessa frase
per tutti (frase 8, 7,5–9,0 s) così il confronto è a parità di testo. Manifest:
`ops/jobs/tts_models_20261109/samples_male_pocket_cosy/upload_manifest.json`.
Le cartelle precedenti (`tts_local_pilot_20261109_male`,
`tts_models_20261109_male`) restano come storico: contengono anche i quattro
video Edge, che non fanno parte del set.

**Lettura**: CosyVoice non sostituisce Pocket, lo affianca. Pocket vince sul
costo ed espone sei voci maschili native; CosyVoice serve quando la voce deve
essere un clone specifico e accetta ~2,7× il tempo reale.

### Note di installazione (perché servono ambienti separati)

`transformers` della pipeline è 4.44.2 e i tre modelli richiedono versioni
incompatibili fra loro: ognuno ha il suo venv (`envs/qwen`, `envs/audio8`,
`envs/cosy`) e la pipeline non viene toccata.

Cose che sono costate tempo, per non ripeterle:

- **CosyVoice 3** importa `whisper`, `lightning`, `rich`, `matplotlib`,
  `pyarrow`, `inflect`, `hydra-core`, `omegaconf`, `diffusers`, `conformer`,
  `pyworld` dalla catena `hyperpyyaml` → `pydoc.locate`. Ogni modulo mancante
  si manifesta solo alla costruzione del modello, non all'`import`.
- `openai-whisper==20231117` non compila con `setuptools` 84 (niente
  `pkg_resources`): serve `pip install "setuptools<81"` e poi
  `--no-build-isolation`.
- Le wheel NVIDIA da `pypi.nvidia.com` vanno in timeout: usare
  `UV_HTTP_TIMEOUT=600` e riprovare, la cache viene riusata.

Riproduzione: `ops/jobs/tts_models_20261109/run_light.sh` (CosyVoice → Audio8,
`LIMIT=10` di default), `run_male_cosy.sh` (clone maschile jean/paul sulle 50
frasi) e `run_remaining.sh` per il run completo 50 frasi.

## 4. Timing e cache

**Timing**: Pocket/Phonon non espongono word boundary nativi. Il bridge
restituisce `boundary_count: 0` e `metadata_path: ""`; non inventa timestamp.
Il pilota è quindi compatibile solo con policy `best_effort`. Una richiesta
`required` deve restare su Edge finché non viene collegato un forced aligner
sul testo e sull'audio prodotto.

**Cache**: il fingerprint v2 non conteneva il provider e avrebbe riusato audio
Edge con la stessa voce. Il nuovo `v3` include
`provider_id + voice + text_hash + language + folder + timing + format`.
L'idempotency key include il provider solo quando il router ne dichiara uno,
quindi l'abilitazione invalida esattamente le lingue instradate e non tocca
le altre. Cambiare `pocket` → `phonon` cambia il provider id
(`local-tts:pocket` → `local-tts:phonon`) e rigenera l'audio.

## 5. Abilitazione

```yaml
voiceover:
  local_tts:
    enabled: true
    engine: pocket          # pocket | phonon | qwen3 | cosyvoice | fish | audio8
    voice: alba             # catalogo Pocket; per gli altri engine è il loro voice id
    max_concurrent: 1       # CPU: 1 è il valore misurato sicuro
    languages: ["en"]       # base code; tutte le altre restano su Edge
    provider_id: local-tts  # diventa local-tts:<engine> nel fingerprint
```

Equivalente via ambiente:

```bash
VELOX_VOICEOVER_LOCAL_TTS_ENABLED=true
VELOX_VOICEOVER_LOCAL_TTS_ENGINE=pocket
VELOX_VOICEOVER_LOCAL_TTS_VOICE=alba
VELOX_VOICEOVER_LOCAL_TTS_LANGUAGES=en   # se il loader supporta le liste da env
```

Requisiti Python:

```bash
python3 -m pip install -r refactored/scripts/bridges/requirements-tts-local.txt
```

Se il sidecar locale non parte, la chiamata **fallisce**: non c'è fallback
silenzioso a Edge, perché produrrebbe audio Edge attribuito a `local-tts`.

## 6. Comandi di verifica

```bash
cd refactored
go build ./internal/...
go test ./internal/capabilities/voiceover/service/ -run 'Fingerprint|Idempotency' -count=1
go test ./internal/app/wiring/voiceover/ -run 'TestLanguageRoutedTTS' -count=1

python3 -m py_compile scripts/bridges/local_tts_bridge/*.py scripts/bridges/tts_local_server.py
PYTHONPATH=scripts/bridges python3 -c \
  'from local_tts_bridge.engines import build_registry; print({k: v.available() for k,v in build_registry().items()})'

python3 ops/jobs/tts_local_pilot_20261109/bench.py --engines pocket --limit 3 --voice alba
```

## 7. Limiti e prossimi passi

1. **Ascolto in cieco** dei sample Pocket vs Edge: è il criterio che decide,
   non l'RTF.
2. **Phonon**: serve hardware con AVX2 o un runner precompilato diverso; su
   questa macchina non è certificabile.
3. **Forced alignment** per i motori locali, se si vuole `timing=required`.
4. **Chunking**: i 24 token attuali sono tarati su Edge. Per Pocket la
   prosodia potrebbe migliorare con 2–3 frasi per chunk: da misurare, non da
   cambiare globalmente.
5. **Qualità multilingue**: fuori perimetro. Le altre 10 lingue restano Edge.
6. **CosyVoice 3**: è l'unico dei modelli grandi che vale la pena cablare. Oggi
   esiste solo come runner di benchmark; per usarlo in produzione serve
   esporlo nel sidecar dietro l'engine `cosyvoice` e decidere dove vive la
   voce di riferimento (oggi è un sample Pocket, quindi il clone eredita la
   prosodia di Pocket).
