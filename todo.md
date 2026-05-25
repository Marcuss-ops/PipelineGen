# Refactoring TODO

## Current Refactoring Tasks

### ✅ fix-nested (DONE — committed 60763e8)
Nested Drive folder structure: `{style}/{prompt-title}/{prompt-title}.mp4`
- Local: `data/images/medievale/old stone cottage countryside/old stone cottage countryside.mp4`
- Drive: `Immagini/medievale/old stone cottage countryside/old stone cottage countryside.mp4`
- Ogni prompt ha la sua subfolder dentro lo stile

### ✅ fix-video (DONE)
- ImageToVideo con Ken Burns zoom (scale + zoompan)
- Output 1920×1080, 7s, 30fps

### ✅ fix-drive (DONE)
- Video caricati su Drive (non immagini)
- Upload in `{style}/{prompt}/{prompt}.mp4`

### ✅ test-all (DONE)
- Build: OK
- Tests: OK (27 packages)
- Live endpoint: 200 OK con Drive links validi

### ⚠️ fix-resolution (PARZIALE)
- Immagini generate a 1024×1024 (limite NVIDIA flux-1-dev, 1920×1080 rifiutato con 422)
- Video output 1920×1080 (ffmpeg scala + Ken Burns zoom)
- Alternative: provare flux-2-klein o altro modello NVIDIA