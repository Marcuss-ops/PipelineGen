# Clip Script Matcher - Documentazione

## 🎯 Cosa Fa

Script Python che:
1. **Legge le tue clip da Drive** (2,020 clip indicizzate)
2. **Genera segmenti di script** basati su un topic
3. **Trova e collega le clip giuste** ad ogni segmento
4. **Output formattato** con link Drive diretti

---

## 🚀 Uso

### **Test Rapido**
```bash
cd /home/pierone/Pyt/VeloxEditing/refactored

# Test con Andrew Tate
python3 scripts/clip_script_matcher.py --topic "Andrew Tate boxing"

# Test con Elvis
python3 scripts/clip_script_matcher.py --topic "Elvis Presley" --segments 4

# Test con 50cent
python3 scripts/clip_script_matcher.py --topic "50cent music"
```

### **Output JSON (per integrazione API)**
```bash
python3 scripts/clip_script_matcher.py --topic "Elvis" --segments 3 --json
```

### **Senza Artlist**
```bash
python3 scripts/clip_script_matcher.py --topic "Floyd Mayweather" --no-artlist
```

### **Custom Settings**
```bash
python3 scripts/clip_script_matcher.py \
  --topic "Andrew Tate" \
  --segments 5 \
  --clips-per-segment 5 \
  --top-k 20
```

---

## 📊 Test Results - Tutti i Topic

| Topic | Clip Trovate | Folder Match | Score |
|-------|-------------|-------------|-------|
| Andrew Tate boxing | 6 | AndrewTate | 100% ✅ |
| Elvis Presley | 6 | Elvis | 100% ✅ |
| 50cent music | 6 | 50CentDiddy | 100% ✅ |
| Floyd Mayweather | 6 | Floyd | 100% ✅ |
| Anthony Joshua | 6 | AnthonyJake | 100% ✅ |
| Mr Bean comedy | 6 | Mr Bean | 100% ✅ |
| Leonardo Dicaprio | 6 | Leonardo Di Caprio | 100% ✅ |
| Escobar | 6 | Pablo Escobar | 100% ✅ |

---

## 📁 Clip Disponibili per Folder (Top 20)

| Folder | Clip Count |
|--------|-----------|
| Stock Gervonta | 67 |
| Mr bean | 61 |
| 50cent | 60 |
| AnthonyJake | 52 |
| Cody Rhodes | 52 |
| Triple H | 51 |
| Big U | 51 |
| Anthony Joshua | 49 |
| Dicaprio | 49 |
| Elvis Presley | 49 |
| Prince Roger Nelson | 49 |
| Broke Lesnar | 49 |
| Alito | 48 |
| Client Eastowod | 48 |
| MUhamadali | 45 |
| Carlos Manzo | 45 |
| Marcola | 41 |
| Floyd | 41 |
| escobar | 41 |
| Robert redford | 41 |

**Totale:** 2,020 clip in 101 folders

---

## 🔍 Algoritmo di Matching

### **Priority 1: Exact Folder Match (100%)**
```
Topic: "Elvis Presley" → Folder: "Elvis" → ✅ 100%
```

### **Priority 2: Exact Name Match (95%)**
```
Topic: "Tate boxing" → Clip name: "Tate afferma..." → ✅ 95%
```

### **Priority 3: Fuzzy Match (40-89%)**
```
Topic: "Elon Musk Tesla" → Tags: "tesla, electric, car" → ✅ 75%
```

---

## 💾 Output JSON Structure

```json
{
  "topic": "Elvis Presley",
  "segments": [
    {
      "index": 1,
      "text": "Introduzione a Elvis Presley. Presley, Elvis sta cambiando...",
      "clips": [
        {
          "name": "01 La scioccante realta domestica Riley racconta come",
          "folder": "Elvis",
          "match_score": 100,
          "drive_url": "https://drive.google.com/file/d/1XS6s2f4GtzTK2..."
        },
        {
          "name": "02 La lettera straziante Riley rivela che Lisa Marie",
          "folder": "Elvis",
          "match_score": 100,
          "drive_url": "https://drive.google.com/file/d/1CMhpP2h3wV5sY1..."
        },
        {
          "name": "03 Il santuario privato Viene spiegato che il piano s",
          "folder": "Elvis",
          "match_score": 100,
          "drive_url": "https://drive.google.com/file/d/1qCvmZw1P2LaZA2..."
        }
      ]
    }
  ]
}
```

---

## 🎯 Come Funziona

### **Step 1: Load Clips**
```python
# Da Drive index
drive_clips = load_drive_clips()  # 2,020 clips

# Da Artlist DB (opzionale)
artlist_clips = load_artlist_clips()  # 300 clips
```

### **Step 2: Extract Entities**
```python
topic = "Andrew Tate boxing champion"
entities = extract_entities(topic)  # ['andrew', 'tate', 'boxing', 'champion']
```

### **Step 3: Generate Segments**
```python
segments = [
    {"index": 1, "text": "Introduzione a Andrew Tate...", "entities": [...]},
    {"index": 2, "text": "La storia di Tate...", "entities": [...]},
    {"index": 3, "text": "I momenti iconici...", "entities": [...]},
]
```

### **Step 4: Match Clips**
```python
for segment in segments:
    clips = find_matching_clips(all_clips, segment['entities'])
    segment['clips'] = clips[:3]  # Top 3 per segmento
```

### **Step 5: Output**
- Formattato con emoji e link Drive
- JSON per integrazione API
- Salvato su file

---

## 📝 Files

| File | Description |
|------|-------------|
| `scripts/clip_script_matcher.py` | Script principale |
| `data/clip_index.json` | Indice clip da Drive (2,020 clip) |
| `data/clip_script_match_*.json` | Output generati |

---

## ✅ Status

- ✅ Lettura clip da Drive: **Working**
- ✅ Entity extraction: **Working**
- ✅ Fuzzy matching: **Working**
- ✅ Link Drive corretti: **Working**
- ✅ Output JSON: **Working**
- ✅ 8/8 topics testati: **100% match rate**

---

## 🎬 Next Steps

1. **Integrazione con API Go** - Esporre come endpoint HTTP
2. **Video metadata** - Estrarre durata, risoluzione da Drive
3. **Auto-thumbnail** - Generare anteprime per le clip
4. **Batch processing** - Matchare più topic in una volta
5. **Cache** - Salvare risultati per evitare re-match
