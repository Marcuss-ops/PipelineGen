# PATCH: Correzione del bug nella durata video con clip intermedie
# Questo file contiene la logica corretta per il fallback matematico

def corrected_fallback_logic(voiceover_duration, M, main_label, status_callback):
    """
    Logica corretta per il fallback matematico quando il posizionamento intelligente fallisce.
    
    PROBLEMA ORIGINALE:
    - Il codice divideva la durata del voiceover per (M + 1)
    - Con 20 minuti di voiceover e 1 clip intermedia: 1200s / (1 + 1) = 600s
    - Processava solo 10 minuti invece di tutti i 20 minuti
    
    SOLUZIONE:
    - Processa l'intera durata del voiceover (20 minuti)
    - Inserisce le clip intermedie nei punti appropriati
    - Continua a processare il resto del voiceover dopo ogni clip intermedia
    """
    
    # Fallback to mathematical approach - FIXED: Process full voiceover duration
    status_callback(f"{main_label} ✅ === RAMO: FALLBACK MATEMATICO CORRETTO ===", False)
    status_callback(f"{main_label} ⚠️ Condizione: Posizionamento intelligente fallito", False)
    status_callback(f"{main_label} ✅ CORREZIONE: Processando intera durata voiceover: {voiceover_duration:.2f}s", False)
    audio_segments = []
    
    # Calculate optimal insertion points for middle clips
    # Distribute middle clips evenly across the voiceover duration
    insertion_interval = voiceover_duration / (M + 1)
    insertion_times = [insertion_interval * (i + 1) for i in range(M)]
    status_callback(f"{main_label} ✅ Tempi inserimento calcolati: {[f'{t:.1f}s' for t in insertion_times]}", False)
    
    # Create audio segments between insertion points
    current_time = 0.0
    for i, insertion_time in enumerate(insertion_times):
        if insertion_time > current_time:
            segment_duration = insertion_time - current_time
            audio_segments.append({
                "start": current_time,
                "duration": segment_duration,
                "followed_by_clip": True
            })
            current_time = insertion_time
            status_callback(f"{main_label} ✅ Segmento {i+1}: start={current_time-segment_duration:.2f}s, duration={segment_duration:.2f}s, followed_by_clip=True", False)
    
    # Add final segment after last insertion (process remaining voiceover)
    if current_time < voiceover_duration:
        final_duration = voiceover_duration - current_time
        audio_segments.append({
            "start": current_time,
            "duration": final_duration,
            "followed_by_clip": False
        })
        status_callback(f"{main_label} ✅ Segmento finale: start={current_time:.2f}s, duration={final_duration:.2f}s, followed_by_clip=False", False)
    
    num_stock_segments = len(audio_segments)
    total_audio_processed = sum(seg["duration"] for seg in audio_segments)
    status_callback(f"{main_label} ✅ Fallback matematico corretto: {num_stock_segments} segmenti, audio totale processato: {total_audio_processed:.2f}s", False)
    
    return audio_segments, num_stock_segments

# ESEMPIO DI UTILIZZO:
# Con 20 minuti (1200s) di voiceover e 1 clip intermedia (M=1):
# - insertion_interval = 1200 / (1 + 1) = 600s
# - insertion_times = [600s]
# - Segmento 1: start=0s, duration=600s, followed_by_clip=True
# - Segmento 2: start=600s, duration=600s, followed_by_clip=False
# - Totale processato: 1200s (tutti i 20 minuti!)