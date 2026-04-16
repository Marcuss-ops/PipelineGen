# Modules/Video - Video Processing

Moduli per la generazione, elaborazione e rendering di video.

## 📁 File Contenuti

### `video_core.py`
Core video processing - funzioni base per manipolazione video.

### `video_audio.py`
Audio processing per video:
- Muxing audio/video
- Estrazione audio
- Analisi proprietà audio
- Funzioni mux_stock_with_voiceover

### `video_clips.py`
Gestione clip video:
- Download e processing clip
- Gestione stock clips
- Operazioni su clip multipli

### `video_effects.py`
Effetti video:
- Transizioni
- Effetti visivi
- Animazioni

### `video_ffmpeg.py`
Wrapper FFmpeg:
- Comandi FFmpeg
- Utility per encoding
- Conversioni formato

### `video_generation.py`
Generazione video principale:
- Pipeline generazione
- Composizione video
- Export finale

### `video_overlays.py`
Overlay e text rendering:
- Sottotitoli
- Testi animati
- Overlay immagini

### `video_processing.py`
Processing video generale:
- Operazioni comuni
- Utility processing
- Helpers vari

### `video_style_effects.py`
Effetti specifici per stili video:
- Stili Rap/Discovery/Crime
- Effetti personalizzati per tipo

### `video_duration_fix.py`
Fix per durata video - utility per correzione durata.

### `Generatevideoparallelized.py`
Generazione video parallelizzata:
- Processing multi-threaded
- Generazione batch
- Ottimizzazioni performance

### `fast_video_generator.py`
Generator veloce - versione ottimizzata per velocità.

### `image_quality_config.py`
Configurazione qualità immagini.

### `image_quality_enhancer.py`
Enhancement qualità immagini per video.

### `image_fallback_search.py`
Ricerca fallback immagini - sistema di fallback per immagini mancanti.

### `transition_downloader.py`
Download transizioni - gestione download transizioni video.

## 🔧 Utilizzo

```python
from modules.video import video_generation, video_audio
# Import moduli video come necessario
```

## 📝 Note

Questi moduli formano il core del sistema di generazione video. La maggior parte della logica video processing è qui.

