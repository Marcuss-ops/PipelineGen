# Clip Script Matcher - Documentazione

## 🎯 Cosa Fa

Script Python che:
1. **Legge le tue clip da Drive**
2. **Genera segmenti di script** basati su un topic
3. **Trova e collega le clip giuste** ad ogni segmento
4. **Output formattato** con link Drive diretti

---

## 🚀 Uso

### **Test Rapido**
```bash
# Test con un topic generico
python3 scripts/clip_script_matcher.py --topic "Artificial Intelligence"

# Test con più segmenti
python3 scripts/clip_script_matcher.py --topic "Space Exploration" --segments 4
```

### **Output JSON (per integrazione API)**
```bash
python3 scripts/clip_script_matcher.py --topic "Technology" --segments 3 --json
```

---

## 📊 Test Results

| Topic | Clip Trovate | Folder Match | Score |
|-------|-------------|-------------|-------|
| Example Topic A | 6 | Folder_A | 100% ✅ |
| Example Topic B | 6 | Folder_B | 100% ✅ |

---

## 🔍 Algoritmo di Matching

### **Priority 1: Exact Folder Match (100%)**
```
Topic: "Topic Name" → Folder: "Topic" → ✅ 100%
```

### **Priority 2: Exact Name Match (95%)**
```
Topic: "Keyword" → Clip name: "Keyword description..." → ✅ 95%
```

### **Priority 3: Fuzzy Match (40-89%)**
```
Topic: "Related Term" → Tags: "tag1, tag2" → ✅ 75%
```

---

## 💾 Output JSON Structure

```json
{
  "topic": "Example Topic",
  "segments": [
    {
      "index": 1,
      "text": "Segment text description...",
      "clips": [
        {
          "name": "clip_name_01",
          "folder": "Folder_Name",
          "match_score": 100,
          "drive_url": "https://drive.google.com/file/d/example-id..."
        }
      ]
    }
  ]
}
```

---

## 📝 Files

| File | Description |
|------|-------------|
| `scripts/clip_script_matcher.py` | Script principale |
| `data/clip_index.json` | Indice clip da Drive |
| `data/clip_script_match_*.json` | Output generati |

---

## ✅ Status

- ✅ Lettura clip da Drive: **Working**
- ✅ Entity extraction: **Working**
- ✅ Fuzzy matching: **Working**
- ✅ Link Drive corretti: **Working**
- ✅ Output JSON: **Working**
