# Come Caricare Script su Google Docs

## ✅ **FILE PRONTO**

**File:** `/home/pierone/Pyt/VeloxEditing/refactored/data/script_docs_andrew_tate.txt`

**Contenuto:**
- ✅ Script completo (80 secondi, 4 segmenti)
- ✅ Timestamp per ogni segmento (0:00-0:20, 0:20-0:40, etc.)
- ✅ 12 Frasi importanti estratte
- ✅ 15 Parole importanti
- ✅ 1 Entity senza testo
- ✅ 14 Clip Drive associate
- ✅ 4 Clip Artlist associate
- ✅ Link diretti a Drive e Artlist

---

## 📋 **ISTRUZIONI PER GOOGLE DOCS**

### **Metodo 1: Copy & Paste (Consigliato)**

1. **Apri il file:**
   ```bash
   cat /home/pierone/Pyt/VeloxEditing/refactored/data/script_docs_andrew_tate.txt
   ```

2. **Copia tutto il contenuto:**
   ```bash
   # Oppure apri con editor visuale
   xdg-open /home/pierone/Pyt/VeloxEditing/refactored/data/script_docs_andrew_tate.txt
   ```

3. **Vai su Google Docs:**
   - Apri browser: https://docs.google.com
   - Clicca su **"+" Documento vuoto"**

4. **Incolla:**
   - Seleziona tutto nel file (Ctrl+A)
   - Copia (Ctrl+C)
   - Vai su Google Docs
   - Incolla (Ctrl+V)

5. **Formatta (opzionale):**
   - Seleziona tutto (Ctrl+A)
   - Font: `Arial` o `Roboto`
   - Size: `11pt`
   - Line spacing: `1.15`

---

### **Metodo 2: Upload diretto (Se hai Google Drive API configurato)**

```bash
# Installa google-api-python-client se non l'hai
pip install google-api-python-client google-auth-httplib2 google-auth-oauthlib

# Usa lo script (richiede credentials.json e token.json)
python3 scripts/generate_and_upload_docs.py --topic "Andrew Tate" --duration 90
```

---

## 📊 **STRUTTURA DEL DOCUMENTO**

```
📝 SCRIPT: Andrew Tate: Mito, Controversie e il Fenomeno Online
================================================================================

🔍 ENTITÀ ESTRATE
├── 📌 FRASI IMPORTANTI (12)
├── 🔑 PAROLE IMPORTANTI (15)
└── 🎨 ENTITY SENZA TESTO (1)

📜 SCRIPT CON TIMESTAMP E CLIP ASSOCIATE
├── 📍 SEGMENTO 1 [0:00 - 0:20]
│   ├── 📜 Testo completo
│   ├── 🔴 FRASI IMPORTANTI → CLIP DRIVE (3)
│   │   ├── 💬 Frase
│   │   ├── 🎬 Nome clip
│   │   ├── 📁 Folder
│   │   ├── 🎯 Match %
│   │   └── 🔗 Drive URL
│   ├── 🟡 FRASI NORMALI → CLIP DRIVE (3)
│   └── 🟢 FRASI VISUALI → ARTLIST (1)
│
├── 📍 SEGMENTO 2 [0:20 - 0:40]
│   └── ... (stessa struttura)
│
├── 📍 SEGMENTO 3 [0:40 - 1:00]
│   └── ... (stessa struttura)
│
└── 📍 SEGMENTO 4 [1:00 - 1:20]
    └── ... (stessa struttura)

✅ RIASSUNTO FINALE
├── 🔍 Entità estratte
└── 🎬 Clip associate
```

---

## 🎬 **ESEMPIO DI COME APPARE IN GOOGLE DOCS**

```
📍 SEGMENTO 1 [0:00 - 0:20]
────────────────────────────────────────────────────────────────────────────────

📜 Andrew Tate è diventato un nome onnipresente sul web...

🔴 FRASI IMPORTANTI → CLIP DRIVE (3):
  💬 "Andrew Tate ha iniziato come trader finanziario."
  🎬 02 Descrizione Questo segmento fornisce il contesto d
  📁 Brazil
  🎯 Entity: 'questo' | Match: 90%
  🔗 https://drive.google.com/file/d/1jW-cB8gFaBi29OY...

  💬 "La sua filosofia è caratterizzata da affermazioni misogine."
  🎬 02 L accusa virale Trippie pubblica una foto di 6ix9i
  📁 6nine
  🎯 Entity: 'virale' | Match: 90%
  🔗 https://drive.google.com/file/d/1HXUaW0KygqdoEX...
```

---

## 🚀 **GENERARE PER ALTRI TOPIC**

```bash
# Elvis Presley
python3 scripts/export_docs_for_google.py --topic "Elvis Presley" --duration 120

# 50cent
python3 scripts/export_docs_for_google.py --topic "50cent music" --duration 90

# Floyd Mayweather
python3 scripts/export_docs_for_google.py --topic "Floyd Mayweather" --duration 60
```

I file verranno salvati in: `/home/pierone/Pyt/VeloxEditing/refactored/data/`

---

## ✅ **RIEPILOGO**

| Elemento | Count |
|----------|-------|
| Segmenti | 4 |
| Durata totale | 80 secondi |
| Timestamp | ✅ (0:00-0:20, 0:20-0:40, 0:40-1:00, 1:00-1:20) |
| Frasi importanti | 12 |
| Parole importanti | 15 |
| Entity senza testo | 1 |
| Drive clips | 14 |
| Artlist clips | 4 |
| TOTALE CLIP | 18 |

**File pronto per Google Docs!** 🎬
