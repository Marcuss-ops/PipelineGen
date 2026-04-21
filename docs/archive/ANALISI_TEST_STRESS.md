# 📊 ANALISI TEST DI STRESS - Risultati e Soluzioni

---

## 🎯 RIEPILOGO RAPIDO

| Test | Risultato | Problemi Trovati |
|------|-----------|------------------|
| **1. Technical Keywords** | ⚠️ PARZIALE | Keywords mancanti, entità errate, 1 scena sola |
| **2. Emotional/Cinematic** | ❌ FAIL | Nessuna emozione rilevata, 1 scena sola |
| **3. Narrative Phases** | ❌ FAIL | 1 scena invece di 5-6, entità sbagliate |

---

## 🔍 TEST 1: Technical Keyword Density

### Input:
> "Il futuro del calcolo quantistico e l'integrazione con i chip neuromorfici nel 2026. Analizza come i qubit superconduttori stiano superando i limiti del silicio tradizionale, permettendo alle AI di elaborare dati a una velocità mai vista prima. Mostra laboratori di ricerca, server farm raffreddate a liquido e simulazioni molecolari."

### ✅ Cosa Funziona:
- **Performance**: 723µs (eccellente, molto sotto 5s)
- **Keywords trovate**: `quantistico`, `silicio`, `2026`, `calcolo`, `simulazioni`, `raffreddate`
- **Category detection**: `tech` ✅ (corretto)
- **Visual Cues**: `show/demonstrate` ✅ (ha rilevato "Mostra")
- **JSON serialization**: Tutti i campi presenti tranne `scene_type`

### ❌ Problemi:

| # | Problema | Dettaglio |
|---|----------|-----------|
| 1 | **Solo 2/8 concetti tecnici trovati** | Manca `qubit`, `neuromorfici`, `server`, `farm`, `laboratori` |
| 2 | **Entità errate** | Rileva "Analizza" e "Mostra" come PERSON_OR_PLACE (sono verbi!) |
| 3 | **1 scena sola** | Non divide in scene tematiche (dovrebbe essere 3-4) |
| 4 | **Keywords poco utili** | `alle`, `prima` non sono utili per clip search |
| 5 | **Manca `scene_type` nel JSON** | Il campo è `type`, non `scene_type` |

### 🔧 Soluzioni:

**Problema 1 - Keywords mancanti:**
```go
// ATTUALE: TF-IDF base che filtra stopwords
// PROBLEMA: "qubit" potrebbe essere filtrato come parola rara
// SOLUZIONE: Aggiungere dizionario tecnico specifico

technicalTerms := map[string]bool{
    "qubit", "quantistico", "neuromorfico", "superconduttore",
    "silicio", "server", "farm", "laboratorio", "molecolare",
}

// Nel parser, prima di estrarre keywords, proteggere questi termini
func protectTechnicalTerms(text string) string {
    for term := range technicalTerms {
        // Assicurati che non vengano filtrati
    }
    return text
}
```

**Problema 2 - Entità errate (verbi scambiati per nomi):**
```go
// ATTUALE: Tutta le parole che iniziano con maiuscola = entità
// PROBLEMA: "Analizza", "Mostra", "Trova" sono verbi imperativi
// SOLUZIONE: Filtrare verbi comuni dall'estrazione entità

falsePositives := map[string]bool{
    "Analizza", "Mostra", "Trova", "Disegna", "Crea", "Concludi",
    "Vediamo", "Guarda", "Immagina", "Considera",
}

func (p *Parser) extractEntities(text string) []SceneEntity {
    var entities []SceneEntity
    words := strings.Fields(text)
    for i, word := range words {
        cleaned := strings.Trim(word, ".,!?;:\"'()[]{}")
        
        // FIX: Escludi falsi positivi
        if falsePositives[cleaned] {
            continue
        }
        
        if len(cleaned) > 2 && cleaned[0] >= 'A' && cleaned[0] <= 'Z' {
            // ... resto del codice
        }
    }
    return entities
}
```

**Problema 3 - Scene splitting:**
```go
// ATTUALE: Divide solo per paragrafi (\n\n) o marker espliciti
// PROBLEMA: Testo single-paragraph non viene diviso
// SOLUZIONE: Aggiungere splitting per frasi lunghe (periodi)

func (p *Parser) splitIntoScenes(text string) []Scene {
    // Metodo 1: Marker espliciti (esistente)
    sections := p.findExplicitSections(text)
    if len(sections) > 1 { return ... }
    
    // Metodo 2: Paragrafi (esistente)
    paragraphs := p.splitByParagraphs(text)
    if len(paragraphs) > 1 { return ... }
    
    // NUOVO Metodo 3: Frasi significative (> 80 chars)
    sentences := p.splitBySentences(text, 80)
    if len(sentences) > 1 {
        for i, sentence := range sentences {
            scenes = append(scenes, Scene{
                SceneNumber: i + 1,
                Type:        p.determineSceneType(sentence, i, len(sentences)),
                Title:       fmt.Sprintf("Scene %d", i+1),
                Text:        sentence,
                Status:      ScenePending,
            })
        }
        return scenes
    }
    
    // Fallback: scena singola
    return []Scene{{...}}
}

func (p *Parser) splitBySentences(text string, minLen int) []string {
    // Divide per "." ma mantiene frasi correlate insieme
    sentences := strings.Split(text, ". ")
    var valid []string
    var current strings.Builder
    
    for _, sentence := range sentences {
        current.WriteString(sentence)
        if current.Len() >= minLen {
            valid = append(valid, current.String())
            current.Reset()
        } else {
            current.WriteString(". ")
        }
    }
    
    if current.Len() > 0 {
        valid = append(valid, current.String())
    }
    
    return valid
}
```

---

## 🔍 TEST 2: Emotional/Cinematic

### Input:
> "Inizia con una riflessione malinconica sulla solitudine nelle grandi città, con pioggia sui vetri e strade vuote all'alba. Poi, improvvisamente, il ritmo cambia: diventa energico, solare e motivazionale. Mostra persone che corrono, iniziano a lavorare con il sorriso e il sole che sorge tra i grattacieli."

### ✅ Cosa Funziona:
- **Visual Cues**: `show/demonstrate`, `scene change` ✅ (ha rilevato "Mostra" e "cambia")
- **Performance**: 351µs

### ❌ Problemi:

| # | Problema | Dettaglio |
|---|----------|-----------|
| 1 | **Nessuna emozione rilevata** | Dovrebbe trovare `sadness` + `joy` |
| 2 | **1 scena sola** | Dovrebbe essere 2 (prima malinconica, seconda energica) |
| 3 | **Category: `general`** | Dovrebbe essere `cinematic` o `lifestyle` |

### 🔧 Soluzioni:

**Problema 1 - Emozioni non rilevate:**
```go
// ATTUALE: Dizionario emozioni molto limitato
emotionWords := map[string][]string{
    "joy":     {"felice", "gioia", "great", "fantastico", "awesome"},
    "sadness": {"triste", "dolore", "sad", "purtroppo", "sfortunatamente"},
}

// PROBLEMA: Manca MOLTISSIME parole emotive italiane
// SOLUZIONE: Espandere dizionario

func (p *Parser) detectEmotions(text string) []string {
    var emotions []string
    textLower := strings.ToLower(text)

    // DIZIONARIO ESPANSO
    emotionWords := map[string][]string{
        "sadness": {
            "malinconic", "malinconia", "solitudine", "triste", "dolore",
            "sad", "purtroppo", "vuoto", "vuota", "pioggia", "grigio",
            "nostalgia", "sconforto",
        },
        "joy": {
            "felice", "gioia", "great", "fantastico", "awesome",
            "sorriso", "energico", "solare", "motivazional", "sole",
            "felicità", "entusiasmo",
        },
        "energy": {
            "energic", "correre", "corrono", "attivo", "dinamico",
            "veloce", "power", "forza",
        },
        "anticipation": {
            "futuro", "prossimo", "will", "going to", "iniziare",
            "sorgere", "sorge", "cambiare", "cambia",
        },
    }

    for emotion, words := range emotionWords {
        for _, word := range words {
            if strings.Contains(textLower, word) {
                emotions = append(emotions, emotion)
                break
            }
        }
    }

    return emotions
}
```

**Problema 2 - Scene splitting (uguale a Test 1):**
- Vedi soluzione sopra (splitBySentences)

---

## 🔍 TEST 3: Narrative Phases

### Input:
> "Guida pratica per creare un brand di successo: 1. Trova la tua nicchia. 2. Disegna un logo minimale. 3. Crea una strategia social aggressiva. 4. Analizza i dati di vendita. Concludi con una call to action per iscriversi al canale."

### ✅ Cosa Funziona:
- **Keywords buone**: `brand`, `logo`, `successo`, `vendita`, `strategia`, `social`
- **Performance**: 528µs
- **Numerazione**: Scene 1 corretta

### ❌ Problemi:

| # | Problema | Dettaglio |
|---|----------|-----------|
| 1 | **1 scena invece di 5-6** | Non riconosce la struttura "1. 2. 3. 4." |
| 2 | **Entità tutte errate** | Guida, Trova, Disegna, Crea, Analizza, Concludi (sono verbi!) |
| 3 | **Category: `general`** | Dovrebbe essere `business` o `education` |
| 4 | **Nessuna emozione** | OK per questo tipo di testo |

### 🔧 Soluzioni:

**Problema 1 - Non riconosce struttura numerata:**
```go
// NUOVO: Pattern per struttura numerata
func (p *Parser) findNumberedSections(text string) []section {
    var sections []section
    
    // Pattern: "1. Titolo" o "1) Titolo" o "Step 1: Titolo"
    pattern := regexp.MustCompile(`(?m)(?:^|\n)\s*(\d+)[.)]\s*(.+?)(?:\n|$)`)
    matches := pattern.FindAllStringSubmatch(text, -1)
    
    if len(matches) >= 2 {
        for _, match := range matches {
            sections = append(sections, section{
                Title: fmt.Sprintf("Step %s: %s", match[1], match[2]),
                Text:  match[0],
            })
        }
        return sections
    }
    
    return nil
}

// Nel splitIntoScenes, aggiungere come primo metodo:
func (p *Parser) splitIntoScenes(text string) []Scene {
    // NUOVO Metodo 0: Struttura numerata
    numbered := p.findNumberedSections(text)
    if len(numbered) > 1 {
        for i, section := range numbered {
            scenes = append(scenes, Scene{
                SceneNumber: i + 1,
                Type:        SceneContent,
                Title:       section.Title,
                Text:        section.Text,
                Status:      ScenePending,
            })
        }
        return scenes
    }
    
    // ... resto dei metodi esistenti
}
```

**Problema 2 - Entità (uguale a Test 1):**
- Vedi soluzione sopra (falsePositives map)

---

## 📈 PRIORITÀ INTERVENTI

### 🔴 CRITICO (Fix Subito):
1. **Entità errate** → Aggiungere mappa `falsePositives` per verbi (10 min)
2. **Scene splitting** → Aggiungere `splitBySentences` + `findNumberedSections` (30 min)
3. **Emozioni** → Espandere dizionario con parole italiane (20 min)

### 🟡 IMPORTANTE (Fix Oggi):
4. **Keywords tecniche** → Proteggere termini tecnici dal filtro (15 min)
5. **JSON field name** → Fixare test (`scene_type` → `type`) (2 min)

### 🟢 DESIDERABILE (Prossima volta):
6. **Category detection** → Aggiungere più categorie + parole chiave italiane
7. **Visual Cues** → Espandere pattern detection

---

## 🛠️ PIANO DI FIX

```
1. Fix entità (falsePositives)          → 10 min
2. Fix scene splitting (sentenze)       → 30 min  
3. Fix emozioni (dizionario espanso)    → 20 min
4. Fix keywords tecniche                → 15 min
5. Fix test JSON field                  → 2 min
                                          ─────────
                                    TOTALE: ~77 min
```

---

## ✅ CONCLUSIONE

I test di stress hanno **rivelato 5 bug reali** nel parser:

1. ❌ **Entità sbagliate** - Verbi scambiati per nomi propri
2. ❌ **Nessuna divisione in scene** - Testo single-paragraph = 1 scena
3. ❌ **Emozioni non rilevate** - Dizionario troppo limitato
4. ⚠️ **Keywords tecniche perse** - TF-IDF filtra termini importanti
5. ⚠️ **Struttura numerata ignorata** - "1. 2. 3. 4." non crea scene separate

**Tutti fixabili in ~1.5 ore di lavoro.**

Vuoi che proceda con le fix ora?
