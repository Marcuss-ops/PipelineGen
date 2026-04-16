# Funzionamento del Multi-Layer Stack (EntityStack)

Questo documento descrive il funzionamento attuale del **multi-layer stack**: il sistema che sovrappone più “entità” (date, nomi, numeri, frasi, immagini) in un unico video, con transizioni fluide e un solo layer attivo in primo piano alla volta.

---

## 1. Panoramica

- **Lato Python (VeloxEditing):** le entità vengono estratte dai dati di editing, raggruppate per vicinanza temporale, convertite nel formato Remotion e passate al renderer.
- **Lato Remotion (my-video):** il componente `EntityStack` riceve la lista `entities`, calcola per ogni frame quale entità è “attiva” e come devono apparire gli altri layer (blur, opacità, scala), e disegna i layer in ordine (background → storia → precedente → **corrente**).

Risultato: un video in cui si vedono più “slide” impilate; quella in primo piano è nitida e a dimensione piena, quelle dietro sono sfocate e rimpicciolite, con transizione animata al cambio entità.

---

## 2. Flusso dati (dove nasce lo stack)

```
associazioni_finali_con_timestamp  (Date, Nomi_Speciali, Numeri, Frasi_Importanti, Entita_Senza_Testo)
        ↓
prepare_entity_stack_groups()      → estrae entità con start_v, dur_v, type, content
        ↓
group_entities_for_stack()         → raggruppa entità vicine (max_distance_seconds = 4.0s)
        ↓
convert_entity_to_remotion_format() → per ogni entità: EntityItem (id, type, content, duration, stili, wrap)
        ↓
render_entity_stack(entities, ...) → inputProps = { entities, backgroundVideo? }
        ↓
Remotion Composition "EntityStack" → <EntityStack entities={...} backgroundVideo={...} />
```

- **`entity_stack_grouper.py`**: `group_entities_for_stack`, `convert_entity_to_remotion_format`, `prepare_entity_stack_groups`.
- **`remotion_renderer.py`**: `render_entity_stack` prepara `inputProps` (entities + backgroundVideo), normalizza immagini e background, invoca Remotion sulla composition `EntityStack`.

---

## 3. Raggruppamento delle entità (`group_entities_for_stack`)

- **Input:** lista di entità con `start_v`, `dur_v`, `type`, `content`, ecc.
- **Logica:** le entità vengono ordinate per `start_v`. Se la distanza tra la **fine** di un’entità e l’**inizio** della successiva è ≤ `max_distance_seconds` (default 4.0s):
  - vengono messe nello **stesso gruppo**;
  - la durata dell’entità precedente viene **estesa** fino all’inizio della successiva, così non c’è buco temporale e la precedente “aspetta” la successiva.
- **Output:** lista di **gruppi**; ogni gruppo è una lista di entità che verranno renderizzate **insieme** in un unico EntityStack (un video = un gruppo).

In questo modo un solo clip Remotion può contenere più entità in sequenza, con transizioni gestite internamente da `EntityStack`.

---

## 4. Conversione nel formato Remotion (`convert_entity_to_remotion_format`)

Ogni entità Python viene trasformata in un **EntityItem** per Remotion:

| Campo Python / logica | Campo Remotion / effetto |
|-----------------------|---------------------------|
| `type` (Date, Nomi_Speciali, …) | `type`: DATE, NAME, NUMBER, PHRASE, IMAGE |
| `start_v`, `dur_v`, `fps` | `duration`: durata in **frames** |
| `content` | `content` (testo o path immagine); per DATE/NUMBER/NAME → maiuscolo |
| Frasi_Importanti | Wrap a **25** caratteri (`_wrap_text_chars(..., 25)`) |
| Nomi_Speciali | Wrap a **18** caratteri (`_wrap_text_chars(..., 18)`) |
| `remotion_style_manager` | `phraseStyle`, `nameStyle`, `dateStyle`, `numberStyle` |
| `bgMode` (se presente) | `bgMode`: BLACK | WHITE |
| Solo prima entità del gruppo | `has_background`: true solo per index 0 (in `prepare_entity_stack_groups`) |

- **Wrap:** evita testo troppo lungo su una riga; le frasi hanno righe più lunghe (25 caratteri) dei nomi (18).
- **Stili:** gli stili Remotion (es. FADEUPWORDS, CLASSIC_V2, TYPEWRITER) vengono mappati dagli ID del `remotion_style_manager`.

### 4.1 Regole Nomi Speciali e Frasi Importanti (font e wrapper)

- **Nomi Speciali (NAME):**
  - Font massimo: in EntityStack 90/75 px (primo/altri), in BackgroundNomiSpeciali TEXT_SIZE 95.
  - Wrapper: **18 caratteri** per riga (`_wrap_text_chars(..., 18)` in grouper; `WRAP_CHAR_LIMIT_NAME = 18` / `WRAP_LIMIT = 18` in Remotion). A capo a meno caratteri rispetto alle frasi.
- **Frasi Importanti (PHRASE):**
  - Font massimo: TEXT_SIZE 95 in MinimalVariations.
  - Wrapper: **25 caratteri** per riga (`_wrap_text_chars(..., 25)` in grouper; `WRAP_CHAR_LIMIT_PHRASE = 25` in EntityStack). Parole più lunghe del limite vengono spezzate per evitare concatenazioni (es. "NELLAPERCEZIONE").

---

## 5. Timeline e “chi è attivo” (lato Remotion)

In `EntityStack.tsx`:

- **Timeline:** per ogni entità si calcolano `startFrame` e `endFrame` cumulando le `duration` (in frames). Opzionalmente si usa `holdFrames` per tenere l’ultimo frame “congelato” (fermo immagine).
- **Per il frame corrente:**
  - si determina **activeIndex** (indice dell’entità il cui intervallo contiene il frame);
  - si calcola **transitionProgress** (0 → 1) nei primi `TRANSITION_DURATION` (32) frames dell’entità attiva: serve per animare entrata della corrente e uscita della precedente.

Quindi in ogni momento c’è **una sola entità “corrente”**; le altre sono “precedente” o “storia” (layer più indietro).

---

## 6. Snapshot dei layer (`layerSnapshot`)

Per ogni frame, per tutti gli indici da 0 fino a `activeIndex` (incluso), si costruisce una riga con:

- **index**, **type**
- **zIndex**, **blur**, **opacity**, **scale**
- **glow** (WHITE | BLACK)

Regole semplificate:

- **Layer corrente (activeIndex):**
  - zIndex alto (9999), blur 0, opacity da 0 → 1 (o da 0.6 → 1 se PHRASE dopo IMAGE), scale da 1.2 → 1.
- **Layer precedente (activeIndex - 1):**
  - zIndex 5000, blur e opacity interpolati con `transitionProgress` (diventa più sfocato e trasparente), scale leggermente ridotta.
- **Layer “storia” (indice < activeIndex - 1):**
  - zIndex 100 + index, blur e opacity fissi (es. blur = profile × 1.2, opacity 0.75), scale leggermente ridotta per dare profondità.

I **blur profile** dipendono dal tipo (DATE, NAME, IMAGE, NUMBER, PHRASE, CLEAN_TYPEWRITER): ad es. NAME ha blur più basso per restare leggibile, IMAGE un po’ più alto per lo sfondo.

---

## 7. Ordine di render (DOM)

- Il **background** (video o griglia) è sempre il primo layer (zIndex 0).
- I layer entità vengono disegnati in un ordine **calcolato** così:
  - si considerano gli indici da 0 a `activeIndex`;
  - l’entità **corrente** (`activeIndex`) viene messa **per ultima** nel DOM, così è in cima allo stack (in primo piano).
- Ordine effettivo: `layerIndicesOrdered` = [0, 1, …, activeIndex-1, activeIndex] ma ordinati in modo che **activeIndex sia ultimo**. Esempio: per activeIndex=2 si disegna prima 0, poi 1, poi 2 (corrente in cima).

Questo garantisce che il layer “corrente” non venga mai coperto dagli altri.

---

## 8. Visibilità e transizioni per layer

Per ogni layer nel loop di render:

- **Scale, opacity, blur, zIndex** derivano da `layerSnapshot` (e da interpolazioni su `transitionProgress` per corrente e precedente).
- **Freeze:** se l’entità ha `holdFrames` e il frame è nella zona di “hold”, il contenuto dell’entità viene congelato (es. con `<Freeze>`) per evitare flicker e dare un fermo immagine finale.
- **PHRASE dopo IMAGE:** ci sono interpolazioni dedicate (opacity/scale più rapide, sottofondo scuro per la frase) per evitare flash bianchi e transizione più fluida.

---

## 9. Tipi di entità e componenti usati

| type (Remotion) | Componente / wrapper | Note |
|----------------|----------------------|------|
| DATE | DateEntityWrapper → Numeric_* (Typewriter, ZoomIn, SlideUp, …) | dateStyle |
| NUMBER | NumberEntityWrapper → Numeric_* | numberStyle |
| NAME | NameEntityWrapper → NomiSpeciali_* (Classic, Typewriter, Highlight) | nameStyle, wrap 18, font max 90/75px |
| PHRASE / PHRASE_MINIMAL | PhraseEntityWrapper → Minimal_* (FadeUpWords, SlideSoft, …) | phraseStyle, wrap 25, sottofondo scuro se corrente |
| IMAGE | ImageEntity → Image_3D_Float_Var5 | content = path/URL immagine |
| CLEAN_TYPEWRITER | CleanTypewriterWrapper | Testo con effetto typewriter |

Font massimi e wrap sono definiti in `EntityStack.tsx` (WRAP_CHAR_LIMIT_NAME/PHRASE, MAX_FONT_SIZE_*) e nei componenti di background (es. BackgroundNomiSpeciali, MinimalVariations).

---

## 10. Background e tema

- **backgroundVideo:** se fornito (da `remotion_renderer` come path in `public/`), viene usato come layer di sfondo sotto tutte le entità. Altrimenti si usa il componente `Background` (griglia chiara/scura).
- **bgMode:** BLACK | WHITE imposta il tema (testo bianco su nero o nero su bianco) e influenza il colore del glow.
- **Glow:** ogni entità ha un glow assegnato (catena alternata WHITE/BLACK); con sfondo WHITE il glow effettivo è forzato a WHITE per contrasto.
- Solo la **prima entità** del gruppo può avere “background” visibile (has_background); le successive sono trasparenti e si appoggiano sulle entità precedenti e sul background condiviso.

---

## 11. Renderer Python (remotion_renderer)

- **render_entity_stack(entities, output_path, background_path=None, …)**  
  - Risolve il progetto Remotion (`my-video`), verifica Node/npm, prepara `inputProps = { entities, backgroundVideo? }`.
  - Copia/normalizza il background e le immagini in `public/` e passa path relativi (es. `entitystack_bg_*.mp4`, `entitystack_img_*`).
  - Calcola `total_duration_frames` come somma delle `duration` delle entità.
  - Invoca Remotion sulla composition **"EntityStack"** con `composition_id="EntityStack"`, le stesse dimensioni/fps del video finale e la durata in frames calcolata.
- **Concurrency:** il numero di istanze Chrome parallele per il render può essere limitato in base al carico di sistema (`_get_render_concurrency`), con un minimo utile di 4 per gli stack lunghi.

### 11.1 History Snapshot (2-layer live)

Per velocizzare, esiste una modalità **history snapshot**:

- Invece di renderizzare tutti i layer “storici”, il renderer crea **PNG di snapshot** (uno per ogni cambio entità).
- Durante il render video si mostrano **solo 2 layer vivi**:
  - **current** (entità attiva),
  - **previous snapshot** (PNG dell’ultima entità, congelata).

Questo riduce il costo per frame e rende i gruppi lunghi più veloci.

Flusso:

```
render_entity_stack_history_snapshots(...) → historySnapshots = [{ startFrame, src }]
render_entity_stack(..., history_snapshots=historySnapshots)
```

Nel componente Remotion `EntityStack`:
- se `historySnapshots` è presente e `activeIndex >= 1`, il layer precedente è un `<Img>` statico (non un componente live);
- altrimenti resta il fallback `Freeze` (snapshot live dell’entità precedente).

---

## 12. Riepilogo

| Concetto | Dove | Cosa fa |
|----------|------|---------|
| Raggruppamento | `entity_stack_grouper.group_entities_for_stack` | Entità vicine (≤4s) nello stesso gruppo; estende durata precedente |
| Formato entità | `entity_stack_grouper.convert_entity_to_remotion_format` | EntityItem con type, duration (frames), content (wrap 18/25), stili |
| Preparazione gruppi | `entity_stack_grouper.prepare_entity_stack_groups` | Estrae entità da associazioni, raggruppa, converte, assegna has_background e background_path |
| Render video | `remotion_renderer.render_entity_stack` | inputProps entities + backgroundVideo, chiamata Remotion composition "EntityStack" |
| Timeline | `EntityStack.tsx` | start/end per entità, activeIndex, transitionProgress, freeze (holdFrames) |
| Layer snapshot | `EntityStack.tsx` | zIndex, blur, opacity, scale, glow per ogni layer 0…activeIndex |
| Ordine DOM | `EntityStack.tsx` | Background (zIndex 0) + layer entità con **corrente per ultima** |
| Contenuto | EntityStack + wrapper (Date, Number, Name, Phrase, Image, CleanTypewriter) | Un componente per tipo, con stili e wrap coerenti con grouper |

---

## 13. Stile multi-layer unico per video e animazione casuale per layer

- **Stile multi-layer unico per video:** a inizio programma (nella pipeline di generazione video, es. `Generatevideoparallelized`) si fa **un solo random** per decidere quale animazione multi-layer stack usare (`DEFAULT`, `BOTTOM_TO_TOP`, `LEFT_TO_RIGHT`, `RIGHT_TO_LEFT`, `TOP_TO_BOTTOM`). Quel valore viene passato a **tutte** le chiamate a `prepare_entity_stack_groups` e a **tutte** le chiamate a `render_entity_stack` per quel video. Così tutti gli EntityStack del video usano lo stesso stile (es. sempre DEFAULT o sempre BOTTOM_TO_TOP).
- **Animazione casuale per ogni layer:** quando lo stile è `DEFAULT`, per ogni entità (layer) si assegna **in random** una variante di animazione in base al tipo: `nameStyle` per NAME (es. CLASSIC_V2, TYPEWRITER_V1, …), `phraseStyle` per PHRASE (es. FADEUPWORDS, SLIDESOFT, …), `dateStyle` per DATE, `numberStyle` per NUMBER. Questo avviene in `convert_entity_to_remotion_format` con `randomize_layer_animation=True` (usato quando `stack_layer_style == 'DEFAULT'`). Se `remotion_style_manager` ha già impostato uno stile per quell’entità, si può rispettare quello oppure sovrascrivere con il random (configurabile).

In sintesi: il **multi-layer stack** è un unico composition Remotion che riceve una lista di entità già raggruppate e convertite; per ogni frame decide chi è “attivo”, calcola lo stato visivo di tutti i layer e li disegna in ordine con la corrente in cima, producendo un video a più “slide” con transizioni e profondità.
