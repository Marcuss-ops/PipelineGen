"""
Video style effects module - Wraps different text effects for Young vs Old styles.
"""

import os
import sys
import logging
import tempfile
import traceback
from typing import Optional, Tuple, Callable, Any
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageFilter
from moviepy import VideoClip, ImageClip, CompositeVideoClip, VideoFileClip, ColorClip, concatenate_videoclips
from moviepy import vfx
try:
    from moviepy import AudioFileClip, AudioClip, CompositeAudioClip, concatenate_audioclips
except ImportError:
    AudioFileClip = None
    AudioClip = None
    CompositeAudioClip = None
    concatenate_audioclips = None
try:
    from scipy.ndimage import zoom
except ImportError:
    zoom = None  # Fallback se scipy non è disponibile

logger = logging.getLogger(__name__)

# Try to download fonts on import if needed
try:
    from font_downloader import check_and_download_fonts
    # Silently check and download fonts
    try:
        check_and_download_fonts()
    except Exception:
        pass  # Don't fail if font download fails
except ImportError:
    pass  # font_downloader not available

# Add the effects directory to path
# Cerca la directory effetti in vari percorsi (incluso nella directory effects/ dello zip estratto)
EFFECTS_DIR = None
effects_dir_candidates = [
    Path("/home/pierone/drive-download-20251216T145424Z-3-001"),
    Path("/opt/VeloxEditing/drive-download-20251216T145424Z-3-001"),
    Path(__file__).parent.parent.parent / "drive-download-20251216T145424Z-3-001",
    Path(__file__).parent / "effects",  # Directory effects/ nello zip estratto
    Path(__file__).parent.parent / "effects",  # Alternativa
    Path(__file__).parent.parent.parent / "effects",  # refactored/effects (path corretto)
]

for candidate in effects_dir_candidates:
    if candidate.exists() and candidate.is_dir():
        EFFECTS_DIR = candidate
        # Aggiungi al path Python per import diretti
        if str(candidate) not in sys.path:
            sys.path.insert(0, str(candidate))
        logger.debug(f"✅ Directory effetti trovata: {EFFECTS_DIR}")
        break

if not EFFECTS_DIR:
    logger.warning(f"⚠️ Directory effetti non trovata. Cercato in: {effects_dir_candidates}")
    logger.warning("   Alcuni effetti video potrebbero non funzionare (modern_quotes_generator, flickering_titles)")


def make_glow_clip(
    rgba_img: "Image.Image",
    duration: float,
    blur_radius: int = 25,
    intensity: float = 2.0,
) -> VideoClip:
    """
    Lightweight glow clip generator (local fallback).
    This replaces the previous dependency on external `flickering_titles.make_glow_clip`.
    """
    try:
        if rgba_img.mode != "RGBA":
            rgba_img = rgba_img.convert("RGBA")

        alpha = rgba_img.split()[3]
        glow_alpha = alpha.filter(ImageFilter.GaussianBlur(blur_radius))

        # Scale alpha by intensity (clamped to 255)
        def _scale(a: int) -> int:
            v = int(a * float(intensity))
            return 255 if v > 255 else (0 if v < 0 else v)

        glow_alpha = glow_alpha.point(_scale)

        glow_img = Image.new("RGBA", rgba_img.size, (255, 255, 255, 0))
        glow_img.putalpha(glow_alpha)

        return ImageClip(np.array(glow_img)).with_duration(duration)
    except Exception as e:
        logger.warning(f"make_glow_clip fallback failed: {e}")
        # Fully transparent fallback
        empty = Image.new("RGBA", rgba_img.size, (255, 255, 255, 0))
        return ImageClip(np.array(empty)).with_duration(duration)


def load_font_safe(font_path: Optional[str] = None, size: int = 80, bold: bool = True, black: bool = False):
    """Safely load font with fallbacks. Prefers Montserrat if available.
    Downloads Montserrat automatically if not found.
    
    Args:
        font_path: Optional custom font path
        size: Font size
        bold: If True, use Montserrat-Bold (default)
        black: If True, use Montserrat-Black (overrides bold)
    """
    # Try to get Montserrat from font_downloader first
    try:
        from font_downloader import get_fonts_dir, check_and_download_fonts
        fonts_dir = get_fonts_dir()
        
        # Ensure fonts are downloaded (silently)
        try:
            check_and_download_fonts()
        except Exception:
            pass  # Don't fail if download fails
        
        # If black is requested, use Montserrat-Black
        if black:
            montserrat_black = fonts_dir / "Montserrat-Black.ttf"
            if montserrat_black.exists():
                try:
                    font = ImageFont.truetype(str(montserrat_black), size)
                    logger.debug(f"Loaded Montserrat-Black font")
                    return font
                except Exception as e:
                    logger.debug(f"Failed to load Montserrat-Black: {e}")
        
        # Try Montserrat Bold or Medium
        font_filename = "Montserrat-Bold.ttf" if bold else "Montserrat-Medium.ttf"
        montserrat_path = fonts_dir / font_filename
        
        if montserrat_path.exists():
            try:
                font = ImageFont.truetype(str(montserrat_path), size)
                logger.debug(f"Loaded Montserrat font: {font_filename}")
                return font
            except Exception as e:
                logger.debug(f"Failed to load {font_filename}: {e}")
        
        # Try Montserrat-Black as fallback (always available)
        montserrat_black = fonts_dir / "Montserrat-Black.ttf"
        if montserrat_black.exists():
            try:
                font = ImageFont.truetype(str(montserrat_black), size)
                logger.debug(f"Using Montserrat-Black as fallback")
                return font
            except Exception as e:
                logger.debug(f"Failed to load Montserrat-Black: {e}")
    except ImportError:
        logger.debug("font_downloader not available, trying alternative paths")
    except Exception as e:
        logger.debug(f"Error loading Montserrat from font_downloader: {e}")
    
    # Try Montserrat from effects directory (FontsVs) if available
    try:
        if EFFECTS_DIR:
            fonts_dir_effects = Path(EFFECTS_DIR) / "FontsVs"
            if fonts_dir_effects.exists():
                if black:
                    montserrat_black = fonts_dir_effects / "Montserrat-Black.ttf"
                    if montserrat_black.exists():
                        try:
                            return ImageFont.truetype(str(montserrat_black), size)
                        except Exception:
                            pass
                font_filename = "Montserrat-Bold.ttf" if bold else "Montserrat-Medium.ttf"
                montserrat_path = fonts_dir_effects / font_filename
                if montserrat_path.exists():
                    try:
                        return ImageFont.truetype(str(montserrat_path), size)
                    except Exception:
                        pass
    except Exception:
        # Best-effort only
        pass
    
    # Use provided font path if available
    if font_path and os.path.exists(font_path):
        try:
            return ImageFont.truetype(font_path, size)
        except Exception:
            pass
    
    # Try Montserrat from config
    try:
        from config import FONT_PATH_DEFAULT, FONT_PATHS
        if FONT_PATH_DEFAULT and os.path.exists(FONT_PATH_DEFAULT):
            try:
                return ImageFont.truetype(FONT_PATH_DEFAULT, size)
            except Exception:
                pass
        
        # Try all font paths from config
        for fp in FONT_PATHS:
            if fp and os.path.exists(fp) and "Montserrat" in fp:
                try:
                    return ImageFont.truetype(fp, size)
                except Exception:
                    continue
    except ImportError:
        pass
    except Exception:
        pass
    
    # Try common system fonts
    for fallback in ["arialbd.ttf", "arial.ttf", "Arial Bold.ttf", "Arial.ttf"]:
        try:
            return ImageFont.truetype(fallback, size)
        except Exception:
            continue
    
    return ImageFont.load_default()


def wrap_text_every_n_chars(text: str, n: int = 20) -> str:
    """Wraps text every n characters, breaking at spaces when possible.
    Se una parola è più lunga di n, la spezza comunque.
    """
    if not text:
        return text
    
    words = text.split()
    lines = []
    current_line = ""
    
    for word in words:
        # Se la parola da sola supera n, spezzala
        if len(word) > n:
            # Aggiungi la parte corrente se c'è
            if current_line:
                lines.append(current_line)
                current_line = ""
            # Spezza la parola lunga
            for i in range(0, len(word), n):
                chunk = word[i:i+n]
                if chunk:
                    lines.append(chunk)
        # Se aggiungere questa parola supererebbe n, inizia nuova riga
        elif current_line and len(current_line) + len(word) + 1 > n:
            lines.append(current_line)
            current_line = word
        else:
            if current_line:
                current_line += " " + word
            else:
                current_line = word
    
    if current_line:
        lines.append(current_line)
    
    return "\n".join(lines)


def create_flickering_title_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 120,  # Font ancora più grande
    text_color: Tuple[int, int, int] = (255, 255, 255),
    max_chars_per_line: int = 20,  # Nuovo parametro per wrap
    video_style: str = "young"  # "young" (rap) or "old"/"discovery" - cursore solo per young
) -> Optional[CompositeVideoClip]:
    """
    Creates a flickering title overlay (Young style for special names).
    Based on flickering_titles.py
    Now uses Montserrat font, smaller size, and wraps every 20 characters.
    """
    try:
        W, H = size
        
        # Wrap text every max_chars_per_line characters
        wrapped_text = wrap_text_every_n_chars(text, max_chars_per_line)
        
        # NOTE: Older versions depended on external `flickering_titles.py`.
        # We now use local implementations (including `make_glow_clip`) so the style
        # works even when the effects directory is missing on the worker.
        
        # Wrapper functions con più spazio tra le righe (line_spacing_multiplier)
        from PIL import Image, ImageDraw, ImageFont
        import numpy as np
        
        def render_full_text_rgba_with_spacing(width, height, text, font, color, tracking_px, line_spacing_multiplier=1.8, shadow_offset=(2, 2)):
            """Versione modificata con più spazio tra le righe e ombra leggera."""
            img = Image.new("RGBA", (width, height), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            lines = text.split("\n")
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * line_spacing_multiplier)  # Aumenta lo spazio
            
            # Calcola larghezze per ogni linea
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            total_h = line_height * len(lines)
            base_y = (height - total_h) // 2
            
            # Ombra leggera (nero semi-trasparente)
            shadow_color = (0, 0, 0, 100)  # Nero con alpha 100/255
            
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = (width - line_w) // 2
                y = base_y + i * line_height  # Usa line_height aumentato
                
                x = base_x
                for ch in line:
                    # Disegna prima l'ombra
                    draw.text((x + shadow_offset[0], y + shadow_offset[1]), ch, font=font, fill=shadow_color)
                    # Poi il testo principale
                    draw.text((x, y), ch, font=font, fill=color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
            
            return img
        
        def render_partial_text_rgba_with_spacing(width, height, text, font, color, tracking_px, max_chars, line_spacing_multiplier=1.8, shadow_offset=(2, 2)):
            """Versione modificata con più spazio tra le righe per typewriter e ombra leggera."""
            img = Image.new("RGBA", (width, height), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            lines = text.split("\n")
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * line_spacing_multiplier)  # Aumenta lo spazio
            
            # Calcola larghezze per ogni linea
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            total_h = line_height * len(lines)
            base_y = (height - total_h) // 2
            
            # Ombra leggera (nero semi-trasparente)
            shadow_color = (0, 0, 0, 100)  # Nero con alpha 100/255
            
            chars_drawn = 0
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = (width - line_w) // 2
                y = base_y + i * line_height  # Usa line_height aumentato
                
                x = base_x
                for ch in line:
                    if chars_drawn >= max_chars:
                        return img
                    # Disegna prima l'ombra
                    draw.text((x + shadow_offset[0], y + shadow_offset[1]), ch, font=font, fill=shadow_color)
                    # Poi il testo principale
                    draw.text((x, y), ch, font=font, fill=color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                    chars_drawn += 1
            
            return img
        
        # Use Montserrat Black (assolutamente) per nomi speciali rap
        font = load_font_safe(font_path, font_size, black=True)
        
        # Calculate tracking (ridotto per lettere più attaccate)
        tracking_px = int(font_size * 0.08)  # Ridotto ulteriormente per lettere più attaccate (da 0.15 a 0.08)
        
        # Line spacing multiplier (più spazio tra le righe)
        line_spacing_multiplier = 1.8  # Aumenta lo spazio del 80%
        
        # Typewriter duration (first part) - leggermente più veloce
        typewriter_duration = min(4.0, duration * 0.6)  # Ridotto da 5.0/0.7 a 4.0/0.6 per essere più veloce
        
        # Modifica make_typewriter_clip per usare render_partial_text_rgba_with_spacing
        # Dobbiamo creare una versione modificata di make_typewriter_clip
        def make_typewriter_clip_with_spacing(text, font, color, tracking_px, duration, fps, line_spacing_multiplier, shadow_offset, video_style="young"):
            """Versione modificata di make_typewriter_clip con più spazio tra righe, ombra e cursore "I" (solo per young)."""
            lines = text.split("\n")
            total_chars = sum(len(line) for line in lines)
            total_chars = max(total_chars, 1)
            
            # Calcola il tempo per ogni carattere
            char_time = duration / total_chars if total_chars > 0 else duration
            
            frames = []
            for n in range(total_chars + 1):
                frame_img = render_partial_text_rgba_with_spacing(
                    W, H, text, font, color, tracking_px, n, line_spacing_multiplier, shadow_offset
                )
                frames.append(np.array(frame_img))
            
            def make_frame(t):
                progress = min(max(t / duration, 0.0), 1.0)
                idx = int(progress * total_chars)
                
                frame = frames[idx].copy()
                
                # Se non abbiamo ancora scritto tutte le lettere, mostra il cursore "I"
                if idx < total_chars:
                    # Calcola la posizione del cursore (dove sarà la prossima lettera)
                    # Renderizza il testo fino a idx caratteri per sapere dove siamo
                    current_text_img = Image.fromarray(frame)
                    
                    # Trova la posizione della prossima lettera
                    # Calcola dove finisce il testo corrente
                    lines_list = text.split("\n")
                    base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
                    line_height = int(base_line_height * line_spacing_multiplier)
                    
                    # Calcola larghezze per ogni linea
                    line_widths = []
                    for line in lines_list:
                        x = 0
                        for ch in line:
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                        line_widths.append(x)
                    
                    total_h = line_height * len(lines_list)
                    base_y = (H - total_h) // 2
                    
                    # Trova quale carattere stiamo per scrivere
                    chars_counted = 0
                    cursor_x = 0
                    cursor_y = 0
                    
                    for i, line in enumerate(lines_list):
                        line_w = line_widths[i]
                        base_x = (W - line_w) // 2
                        y = base_y + i * line_height
                        
                        x = base_x
                        for ch in line:
                            if chars_counted == idx:
                                # Questa è la posizione del cursore
                                cursor_x = x
                                cursor_y = y
                                break
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                            chars_counted += 1
                        if chars_counted == idx:
                            break
                    
                    # Disegna il cursore "I" lampeggiante (solo per stile young/rap)
                    # Il cursore lampeggia più lentamente per essere più visibile
                    if video_style.lower() == "young":  # Solo per stile rap
                        blink_cycle = (t * 1.5) % 2.0  # Ciclo più lento (circa 0.67s on, 0.67s off)
                        if blink_cycle < 1.0:  # Visibile per più tempo
                            cursor_img = Image.fromarray(frame)
                            draw = ImageDraw.Draw(cursor_img)
                            
                            # Cursore ripristinato come prima (più piccolo)
                            cursor_height = int(font_size * 1.1)  # 110% dell'altezza del font (ripristinato)
                            cursor_width = 12  # Larghezza del cursore ripristinata a 12px
                            
                            # Disegna prima un bordo nero per contrasto (ripristinato)
                            border_rect = [
                                cursor_x - cursor_width // 2 - 2,
                                cursor_y - 3,
                                cursor_x + cursor_width // 2 + 2,
                                cursor_y + cursor_height + 3
                            ]
                            draw.rectangle(border_rect, fill=(0, 0, 0, 180))  # Bordo nero (ripristinato)
                            
                            # Disegna il cursore verticale principale (ripristinato)
                            cursor_rect = [
                                cursor_x - cursor_width // 2,
                                cursor_y,
                                cursor_x + cursor_width // 2,
                                cursor_y + cursor_height
                            ]
                            draw.rectangle(cursor_rect, fill=color)  # Stesso colore del testo
                            
                            frame = np.array(cursor_img)
                
                return frame
            
            from moviepy import VideoClip
            return VideoClip(make_frame, duration=duration)
        
        # Shadow offset leggero
        shadow_offset = (2, 2)
        
        # Create typewriter clip with wrapped text e più spazio tra righe
        typewriter_clip = make_typewriter_clip_with_spacing(
            wrapped_text, font, text_color, tracking_px, typewriter_duration, fps, line_spacing_multiplier, shadow_offset, video_style
        )
        typewriter_clip = typewriter_clip.with_position("center").with_start(0)
        
        # Dynamic glow during typewriter (glow meno luminoso)
        # Dobbiamo anche modificare make_dynamic_glow_clip per usare render_partial_text_rgba_with_spacing
        def make_dynamic_glow_clip_with_spacing(text, font, color, tracking_px, duration, fps, blur_radius, intensity, line_spacing_multiplier, shadow_offset):
            """Versione modificata con più spazio tra righe e ombra."""
            lines = text.split("\n")
            total_chars = sum(len(line) for line in lines)
            total_chars = max(total_chars, 1)
            
            glow_frames = []
            for n in range(total_chars + 1):
                partial_text_img = render_partial_text_rgba_with_spacing(
                    W, H, text, font, color, tracking_px, n, line_spacing_multiplier, shadow_offset
                )
                if n > 0:
                    from PIL import ImageFilter
                    alpha = partial_text_img.split()[3]
                    glow_alpha = alpha.filter(ImageFilter.GaussianBlur(blur_radius))
                    glow_alpha = glow_alpha.point(lambda a: int(a * intensity))
                    glow_img = Image.new("RGBA", partial_text_img.size, (255, 255, 255, 0))
                    glow_img.putalpha(glow_alpha)
                else:
                    glow_img = Image.new("RGBA", (W, H), (255, 255, 255, 0))
                glow_frames.append(np.array(glow_img))
            
            def make_frame(t):
                progress = min(max(t / duration, 0.0), 1.0)
                idx = int(progress * total_chars)
                return glow_frames[idx]
            
            from moviepy import VideoClip
            return VideoClip(make_frame, duration=duration)
        
        dynamic_glow_clip = make_dynamic_glow_clip_with_spacing(
            wrapped_text, font, text_color, tracking_px, typewriter_duration, fps,
            blur_radius=15, intensity=1.3, line_spacing_multiplier=line_spacing_multiplier, shadow_offset=shadow_offset  # Glow meno sfocato (blur 15 invece di 25)
        )
        dynamic_glow_clip = dynamic_glow_clip.with_position("center").with_start(0)
        
        # Static glow after typewriter (glow meno luminoso e molto più attaccato) con più spazio tra righe
        full_text_img = render_full_text_rgba_with_spacing(W, H, wrapped_text, font, text_color, tracking_px, line_spacing_multiplier, shadow_offset)
        static_glow_duration = max(0.1, duration - typewriter_duration)
        static_glow_clip = make_glow_clip(
            full_text_img, static_glow_duration, blur_radius=15, intensity=1.3  # Glow meno sfocato (blur 15 invece di 25)
        )
        static_glow_clip = static_glow_clip.with_position("center").with_start(typewriter_duration)
        
        # Flickering flashes
        flashes = []
        text_array = np.array(full_text_img)
        text_clip = ImageClip(text_array).with_duration(0.4).with_position("center")
        
        t = typewriter_duration + 0.3
        while t < duration - 0.5:
            flashes.append(text_clip.with_start(t))
            t += 0.25
        
        # Background
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception:
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        else:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        # Composite all
        final = CompositeVideoClip(
            [bg_clip, dynamic_glow_clip, static_glow_clip, typewriter_clip] + flashes,
            size=(W, H)
        ).with_duration(duration)
        
        # Aggiungi audio solo durante l'animazione typewriter (parole importanti)
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "parole_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                
                # Taglia l'audio alla durata del typewriter
                if audio_clip.duration > typewriter_duration:
                    audio_clip = audio_clip.subclipped(0, typewriter_duration)
                else:
                    # Se l'audio è più corto del typewriter, loopalo
                    loops_needed = int(typewriter_duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, typewriter_duration)
                
                # Crea un audio clip silenzioso per il resto della durata
                if AudioClip and CompositeAudioClip:
                    silent_audio = AudioClip(lambda t: [0, 0], duration=duration - typewriter_duration, fps=audio_clip.fps)
                    # Combina audio typewriter + silenzio
                    final_audio = CompositeAudioClip([audio_clip.with_start(0), silent_audio.with_start(typewriter_duration)])
                    # Applica l'audio al video usando with_audio invece di set_audio
                    final = final.with_audio(final_audio)
                    logger.debug(f"Audio aggiunto per typewriter: {audio_path}, durata: {typewriter_duration}s")
                else:
                    # Fallback: usa solo l'audio typewriter
                    final = final.with_audio(audio_clip)
                    logger.debug(f"Audio aggiunto (solo typewriter): {audio_path}, durata: {typewriter_duration}s")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio: {e}")
                logger.warning(traceback.format_exc())
        else:
            if not os.path.exists(audio_path):
                logger.warning(f"File audio non trovato: {audio_path}")
            else:
                logger.warning("AudioFileClip non disponibile")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating flickering title overlay: {e}")
        logger.error(traceback.format_exc())
        return None
        
        # Wrapper functions con più spazio tra le righe (line_spacing_multiplier)
        from PIL import Image, ImageDraw, ImageFont
        import numpy as np
        
        def render_full_text_rgba_with_spacing(width, height, text, font, color, tracking_px, line_spacing_multiplier=1.8, shadow_offset=(2, 2)):
            """Versione modificata con più spazio tra le righe e ombra leggera."""
            img = Image.new("RGBA", (width, height), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            lines = text.split("\n")
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * line_spacing_multiplier)  # Aumenta lo spazio
            
            # Calcola larghezze per ogni linea
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            total_h = line_height * len(lines)
            base_y = (height - total_h) // 2
            
            # Ombra leggera (nero semi-trasparente)
            shadow_color = (0, 0, 0, 100)  # Nero con alpha 100/255
            
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = (width - line_w) // 2
                y = base_y + i * line_height  # Usa line_height aumentato
                
                x = base_x
                for ch in line:
                    # Disegna prima l'ombra
                    draw.text((x + shadow_offset[0], y + shadow_offset[1]), ch, font=font, fill=shadow_color)
                    # Poi il testo principale
                    draw.text((x, y), ch, font=font, fill=color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
            
            return img
        
        def render_partial_text_rgba_with_spacing(width, height, text, font, color, tracking_px, max_chars, line_spacing_multiplier=1.8, shadow_offset=(2, 2)):
            """Versione modificata con più spazio tra le righe per typewriter e ombra leggera."""
            img = Image.new("RGBA", (width, height), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            lines = text.split("\n")
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * line_spacing_multiplier)  # Aumenta lo spazio
            
            # Calcola larghezze per ogni linea
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            total_h = line_height * len(lines)
            base_y = (height - total_h) // 2
            
            # Ombra leggera (nero semi-trasparente)
            shadow_color = (0, 0, 0, 100)  # Nero con alpha 100/255
            
            chars_drawn = 0
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = (width - line_w) // 2
                y = base_y + i * line_height  # Usa line_height aumentato
                
                x = base_x
                for ch in line:
                    if chars_drawn >= max_chars:
                        return img
                    # Disegna prima l'ombra
                    draw.text((x + shadow_offset[0], y + shadow_offset[1]), ch, font=font, fill=shadow_color)
                    # Poi il testo principale
                    draw.text((x, y), ch, font=font, fill=color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                    chars_drawn += 1
            
            return img
        
        # Use Montserrat Bold
        font = load_font_safe(font_path, font_size, bold=True)
        
        # Calculate tracking (ridotto per lettere più attaccate)
        tracking_px = int(font_size * 0.08)  # Ridotto ulteriormente per lettere più attaccate (da 0.15 a 0.08)
        
        # Line spacing multiplier (più spazio tra le righe)
        line_spacing_multiplier = 1.8  # Aumenta lo spazio del 80%
        
        # Typewriter duration (first part) - leggermente più veloce
        typewriter_duration = min(4.0, duration * 0.6)  # Ridotto da 5.0/0.7 a 4.0/0.6 per essere più veloce
        
        # Modifica make_typewriter_clip per usare render_partial_text_rgba_with_spacing
        # Dobbiamo creare una versione modificata di make_typewriter_clip
        def make_typewriter_clip_with_spacing(text, font, color, tracking_px, duration, fps, line_spacing_multiplier, shadow_offset, video_style="young"):
            """Versione modificata di make_typewriter_clip con più spazio tra righe, ombra e cursore "I" (solo per young)."""
            lines = text.split("\n")
            total_chars = sum(len(line) for line in lines)
            total_chars = max(total_chars, 1)
            
            # Calcola il tempo per ogni carattere
            char_time = duration / total_chars if total_chars > 0 else duration
            
            frames = []
            for n in range(total_chars + 1):
                frame_img = render_partial_text_rgba_with_spacing(
                    W, H, text, font, color, tracking_px, n, line_spacing_multiplier, shadow_offset
                )
                frames.append(np.array(frame_img))
            
            def make_frame(t):
                progress = min(max(t / duration, 0.0), 1.0)
                idx = int(progress * total_chars)
                
                frame = frames[idx].copy()
                
                # Se non abbiamo ancora scritto tutte le lettere, mostra il cursore "I"
                if idx < total_chars:
                    # Calcola la posizione del cursore (dove sarà la prossima lettera)
                    # Renderizza il testo fino a idx caratteri per sapere dove siamo
                    current_text_img = Image.fromarray(frame)
                    
                    # Trova la posizione della prossima lettera
                    # Calcola dove finisce il testo corrente
                    lines_list = text.split("\n")
                    base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
                    line_height = int(base_line_height * line_spacing_multiplier)
                    
                    # Calcola larghezze per ogni linea
                    line_widths = []
                    for line in lines_list:
                        x = 0
                        for ch in line:
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                        line_widths.append(x)
                    
                    total_h = line_height * len(lines_list)
                    base_y = (H - total_h) // 2
                    
                    # Trova quale carattere stiamo per scrivere
                    chars_counted = 0
                    cursor_x = 0
                    cursor_y = 0
                    
                    for i, line in enumerate(lines_list):
                        line_w = line_widths[i]
                        base_x = (W - line_w) // 2
                        y = base_y + i * line_height
                        
                        x = base_x
                        for ch in line:
                            if chars_counted == idx:
                                # Questa è la posizione del cursore
                                cursor_x = x
                                cursor_y = y
                                break
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                            chars_counted += 1
                        if chars_counted == idx:
                            break
                    
                    # Disegna il cursore "I" lampeggiante (solo per stile young/rap)
                    # Il cursore lampeggia più lentamente per essere più visibile
                    if video_style.lower() == "young":  # Solo per stile rap
                        blink_cycle = (t * 1.5) % 2.0  # Ciclo più lento (circa 0.67s on, 0.67s off)
                        if blink_cycle < 1.0:  # Visibile per più tempo
                            cursor_img = Image.fromarray(frame)
                            draw = ImageDraw.Draw(cursor_img)
                            
                            # Cursore ripristinato come prima (più piccolo)
                            cursor_height = int(font_size * 1.1)  # 110% dell'altezza del font (ripristinato)
                            cursor_width = 12  # Larghezza del cursore ripristinata a 12px
                            
                            # Disegna prima un bordo nero per contrasto (ripristinato)
                            border_rect = [
                                cursor_x - cursor_width // 2 - 2,
                                cursor_y - 3,
                                cursor_x + cursor_width // 2 + 2,
                                cursor_y + cursor_height + 3
                            ]
                            draw.rectangle(border_rect, fill=(0, 0, 0, 180))  # Bordo nero (ripristinato)
                            
                            # Disegna il cursore verticale principale (ripristinato)
                            cursor_rect = [
                                cursor_x - cursor_width // 2,
                                cursor_y,
                                cursor_x + cursor_width // 2,
                                cursor_y + cursor_height
                            ]
                            draw.rectangle(cursor_rect, fill=color)  # Stesso colore del testo
                            
                            frame = np.array(cursor_img)
                
                return frame
            
            from moviepy import VideoClip
            return VideoClip(make_frame, duration=duration)
        
        # Shadow offset leggero
        shadow_offset = (2, 2)
        
        # Create typewriter clip with wrapped text e più spazio tra righe
        typewriter_clip = make_typewriter_clip_with_spacing(
            wrapped_text, font, text_color, tracking_px, typewriter_duration, fps, line_spacing_multiplier, shadow_offset, video_style
        )
        typewriter_clip = typewriter_clip.with_position("center").with_start(0)
        
        # Dynamic glow during typewriter (glow meno luminoso)
        # Dobbiamo anche modificare make_dynamic_glow_clip per usare render_partial_text_rgba_with_spacing
        def make_dynamic_glow_clip_with_spacing(text, font, color, tracking_px, duration, fps, blur_radius, intensity, line_spacing_multiplier, shadow_offset):
            """Versione modificata con più spazio tra righe e ombra."""
            lines = text.split("\n")
            total_chars = sum(len(line) for line in lines)
            total_chars = max(total_chars, 1)
            
            glow_frames = []
            for n in range(total_chars + 1):
                partial_text_img = render_partial_text_rgba_with_spacing(
                    W, H, text, font, color, tracking_px, n, line_spacing_multiplier, shadow_offset
                )
                if n > 0:
                    from PIL import ImageFilter
                    alpha = partial_text_img.split()[3]
                    glow_alpha = alpha.filter(ImageFilter.GaussianBlur(blur_radius))
                    glow_alpha = glow_alpha.point(lambda a: int(a * intensity))
                    glow_img = Image.new("RGBA", partial_text_img.size, (255, 255, 255, 0))
                    glow_img.putalpha(glow_alpha)
                else:
                    glow_img = Image.new("RGBA", (W, H), (255, 255, 255, 0))
                glow_frames.append(np.array(glow_img))
            
            def make_frame(t):
                progress = min(max(t / duration, 0.0), 1.0)
                idx = int(progress * total_chars)
                return glow_frames[idx]
            
            from moviepy import VideoClip
            return VideoClip(make_frame, duration=duration)
        
        dynamic_glow_clip = make_dynamic_glow_clip_with_spacing(
            wrapped_text, font, text_color, tracking_px, typewriter_duration, fps,
            blur_radius=15, intensity=1.3, line_spacing_multiplier=line_spacing_multiplier, shadow_offset=shadow_offset  # Glow meno sfocato (blur 15 invece di 25)
        )
        dynamic_glow_clip = dynamic_glow_clip.with_position("center").with_start(0)
        
        # Static glow after typewriter (glow meno luminoso e molto più attaccato) con più spazio tra righe
        full_text_img = render_full_text_rgba_with_spacing(W, H, wrapped_text, font, text_color, tracking_px, line_spacing_multiplier, shadow_offset)
        static_glow_duration = max(0.1, duration - typewriter_duration)
        static_glow_clip = make_glow_clip(
            full_text_img, static_glow_duration, blur_radius=15, intensity=1.3  # Glow meno sfocato (blur 15 invece di 25)
        )
        static_glow_clip = static_glow_clip.with_position("center").with_start(typewriter_duration)
        
        # Flickering flashes
        flashes = []
        text_array = np.array(full_text_img)
        text_clip = ImageClip(text_array).with_duration(0.4).with_position("center")
        
        t = typewriter_duration + 0.3
        while t < duration - 0.5:
            flashes.append(text_clip.with_start(t))
            t += 0.25
        
        # Background
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception:
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        else:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        # Composite all
        final = CompositeVideoClip(
            [bg_clip, dynamic_glow_clip, static_glow_clip, typewriter_clip] + flashes,
            size=(W, H)
        ).with_duration(duration)
        
        # Aggiungi audio solo durante l'animazione typewriter (parole importanti)
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "parole_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                
                # Taglia l'audio alla durata del typewriter
                if audio_clip.duration > typewriter_duration:
                    audio_clip = audio_clip.subclipped(0, typewriter_duration)
                else:
                    # Se l'audio è più corto del typewriter, loopalo
                    loops_needed = int(typewriter_duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, typewriter_duration)
                
                # Crea un audio clip silenzioso per il resto della durata
                if AudioClip and CompositeAudioClip:
                    silent_audio = AudioClip(lambda t: [0, 0], duration=duration - typewriter_duration, fps=audio_clip.fps)
                    # Combina audio typewriter + silenzio
                    final_audio = CompositeAudioClip([audio_clip.with_start(0), silent_audio.with_start(typewriter_duration)])
                    # Applica l'audio al video usando with_audio invece di set_audio
                    final = final.with_audio(final_audio)
                    logger.debug(f"Audio aggiunto per typewriter: {audio_path}, durata: {typewriter_duration}s")
                else:
                    # Fallback: usa solo l'audio typewriter
                    final = final.with_audio(audio_clip)
                    logger.debug(f"Audio aggiunto (solo typewriter): {audio_path}, durata: {typewriter_duration}s")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio: {e}")
                import traceback
                logger.warning(traceback.format_exc())
        else:
            if not os.path.exists(audio_path):
                logger.warning(f"File audio non trovato: {audio_path}")
            else:
                logger.warning("AudioFileClip non disponibile")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating flickering title overlay: {e}")
        logger.error(traceback.format_exc())
        return None

def create_cinematic_text_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 80,
    text_color: Tuple[int, int, int] = (255, 255, 255),
    effect_type: str = "scramble",  # "scramble" or "permutation"
    show_red_rectangle: bool = False  # Set to False to remove red rectangle for Discovery
) -> Optional[CompositeVideoClip]:
    """
    Creates a cinematic text overlay (Young style for important phrases).
    Based on cinematic_text_effects.py
    """
    try:
        W, H = size
        
        sys.path.insert(0, str(EFFECTS_DIR))
        try:
            import cinematic_text_effects  # type: ignore
            from cinematic_text_effects import (  # type: ignore
                calculate_char_positions,
                make_scramble_frame,
                make_permutation_frame
            )
        except ImportError as e:
            logger.error(f"Could not import cinematic_text_effects functions: {e}")
            return None
        
        # Load font
        font = load_font_safe(font_path, font_size, bold=True, black=False)
        
        # FIX: Limita a massimo 5 righe per Frasi Importanti
        # Wrappa il testo e limita a 5 righe prima di passarlo a calculate_char_positions
        wrapped_text = wrap_text_every_n_chars(text, n=25)
        lines = wrapped_text.split("\n")
        if len(lines) > 5:
            lines = lines[:5]
            # Aggiungi "..." se il testo è stato troncato
            if len(lines) == 5:
                lines[-1] = lines[-1] + "..."
            text = "\n".join(lines)
        
        # Calculate character positions (wraps every 25 chars as per cinematic_text_effects defaults)
        char_infos = calculate_char_positions(text, font, tracking=5, max_chars_per_line=25)
        
        # Load background if provided
        bg_clip = None
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception as e:
                logger.warning(f"Could not load background: {e}")
                bg_clip = None
        
        # Create frame function
        def make_frame(t):
            """Frame using cinematic text effects."""
            bg_frame = None
            if bg_clip is not None:
                try:
                    bg_frame = bg_clip.get_frame(t)
                except Exception:
                    bg_frame = None
            
            # Use scramble or permutation based on effect_type
            if effect_type == "permutation":
                frame = make_permutation_frame(t, char_infos, font, bg_frame)
            else:  # default to scramble
                frame = make_scramble_frame(t, char_infos, font, bg_frame)
            
            if frame is not None:
                # OPTIMIZATION: Avoid PIL conversion if frame is already numpy array
                if isinstance(frame, np.ndarray):
                    img_array = frame.copy()
                else:
                    from PIL import Image
                    pil_img = Image.fromarray(frame)
                    img_array = np.array(pil_img)
                
                # Remove red rectangle for Discovery style (frasi importanti)
                if not show_red_rectangle:
                    # Create mask for red pixels (red rectangle)
                    # Red rectangle typically has high red channel and low green/blue
                    red_mask = (img_array[:, :, 0] > 200) & (img_array[:, :, 1] < 50) & (img_array[:, :, 2] < 50)
                    
                    # Make red pixels transparent or match background
                    if bg_frame is not None:
                        # Replace red with background
                        img_array[red_mask] = bg_frame[red_mask]
                    else:
                        # Make red pixels transparent (black background)
                        img_array[red_mask] = [0, 0, 0, 0] if img_array.shape[2] == 4 else [0, 0, 0]
                
                # Move text higher for Discovery style (frasi importanti)
                shift_up_pixels = 220  # Move text 220px higher (aumentato da 150 per posizionare più in alto)
                
                # Start with background
                if bg_frame is not None:
                    new_img_array = bg_frame.copy()
                    # Ensure same number of channels as img_array
                    if new_img_array.shape[2] == 3 and img_array.shape[2] == 4:
                        # Add alpha channel to background
                        alpha_channel = np.ones((H, W), dtype=np.uint8) * 255
                        new_img_array = np.dstack([new_img_array, alpha_channel])
                    elif new_img_array.shape[2] == 4 and img_array.shape[2] == 3:
                        # Remove alpha from background
                        new_img_array = new_img_array[:, :, :3]
                else:
                    new_img_array = np.zeros((H, W, img_array.shape[2]), dtype=img_array.dtype)
                
                # Extract alpha channel to find text pixels
                if img_array.shape[2] == 4:
                    alpha = img_array[:, :, 3]
                else:
                    alpha = np.ones((H, W), dtype=np.uint8) * 255
                
                # Find where text is (non-transparent pixels, excluding red rectangle)
                text_mask = alpha > 10  # Threshold for text pixels
                
                # Shift text up: OPTIMIZED - use numpy slicing instead of pixel-by-pixel loop
                # This is much faster than the previous loop
                if shift_up_pixels > 0 and shift_up_pixels < H:
                    # Source region (where text is in original position)
                    source_region = img_array[:H-shift_up_pixels, :]
                    source_mask = text_mask[:H-shift_up_pixels, :]
                    
                    # Destination region (where text should be after shift)
                    dest_y_start = shift_up_pixels
                    dest_y_end = H
                    
                    # Copy text pixels using numpy boolean indexing (much faster)
                    # Only copy pixels where there's text (source_mask)
                    if source_mask.any():
                        # Create destination mask
                        dest_mask = np.zeros((H, W), dtype=bool)
                        dest_mask[dest_y_start:dest_y_end, :] = source_mask
                        
                        # Copy pixels where mask is True
                        new_img_array[dest_mask] = source_region[source_mask]
                
                frame = new_img_array
            
            return frame
        
        overlay = VideoClip(make_frame, duration=duration)
        
        # Background
        if bg_clip is None:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        final = CompositeVideoClip([bg_clip, overlay], size=(W, H)).with_duration(duration)
        
        # Aggiungi audio per frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, duration)
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per cinematic text: {audio_path}")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio cinematic text: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating cinematic text overlay: {e}")
        logger.error(traceback.format_exc())
        return None


def create_simple_discovery_phrase_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 72,
    text_color: Tuple[int, int, int] = (255, 255, 255),
    zoom: float = 1.015,
    fade_in: float = 0.35,
    fade_out: float = 0.20,
) -> Optional[CompositeVideoClip]:
    """
    Discovery/Old: overlay semplice e fisso (PIL static + leggero zoom + FadeIn/FadeOut).
    Obiettivo: sostituire cinematic_text_effects (molto costoso) con un rendering veloce e consistente.
    """
    try:
        W, H = size
        duration = max(0.1, float(duration))

        # Background (opzionale)
        bg_clip = None
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration).with_fps(fps)
                
                # Blur più leggero (visivamente): applichiamo un leggero sharpening per ridurre l'effetto "troppo sfocato"
                # senza re-render costosi o dipendere da un blur forte upstream.
                try:
                    import cv2
                    
                    def _unsharp(frame):
                        if frame is None:
                            return frame
                        if frame.dtype != np.uint8:
                            frame = np.clip(frame, 0, 255).astype("uint8")
                        blurred = cv2.GaussianBlur(frame, (0, 0), 1.0)
                        return cv2.addWeighted(frame, 1.22, blurred, -0.22, 0)
                    
                    bg_clip = bg_clip.image_transform(_unsharp)
                except Exception:
                    pass
            except Exception as e:
                logger.warning(f"Could not load background for simple discovery overlay: {e}")
                bg_clip = None

        if bg_clip is None:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration).with_fps(fps)

        # Render testo una sola volta (PIL) → ImageClip
        from PIL import Image, ImageDraw

        font = load_font_safe(font_path, font_size, bold=True, black=False)

        # Wrap: fisso e prevedibile (richiesto: 25 caratteri)
        # FIX: Limita a massimo 5 righe per Frasi Importanti
        wrapped_text = wrap_text_every_n_chars(text, n=25)
        lines = [ln.strip() for ln in wrapped_text.split("\n") if ln.strip()]
        # Limita a massimo 5 righe
        if len(lines) > 5:
            lines = lines[:5]
            # Aggiungi "..." se il testo è stato troncato
            if len(lines) == 5 and len(wrapped_text.split("\n")) > 5:
                lines[-1] = lines[-1] + "..."
        if not lines:
            return None

        img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
        draw = ImageDraw.Draw(img)

        # Calcolo layout multi-line
        line_bboxes = [draw.textbbox((0, 0), ln, font=font) for ln in lines]
        line_widths = [b[2] - b[0] for b in line_bboxes]
        line_heights = [b[3] - b[1] for b in line_bboxes]
        line_gap = int(font_size * 0.30)
        total_h = sum(line_heights) + max(0, len(lines) - 1) * line_gap

        # Posizione: un po' più alta (coerente con Discovery attuale)
        y = max(30, int(H * 0.40) - total_h // 2)

        shadow = (0, 0, 0, 180)
        for i, ln in enumerate(lines):
            w = line_widths[i]
            h = line_heights[i]
            x = (W - w) // 2
            draw.text((x + 3, y + 3), ln, font=font, fill=shadow)
            draw.text((x, y), ln, font=font, fill=(*text_color, 255))
            y += h + line_gap

        text_clip = ImageClip(np.array(img)).with_duration(duration).with_fps(fps)

        # Leggero zoom in (Ken Burns) + fade
        if zoom and zoom > 1.0:
            text_clip = text_clip.resized(lambda t: 1.0 + (zoom - 1.0) * (t / duration))

        # Fade in/out (evita vfx per ridurre overhead, ma qui va bene)
        try:
            from moviepy import vfx
            fi = min(max(0.0, fade_in), duration / 2)
            fo = min(max(0.0, fade_out), duration / 2)
            if fi > 0:
                text_clip = text_clip.with_effects([vfx.FadeIn(fi)])
            if fo > 0:
                text_clip = text_clip.with_effects([vfx.FadeOut(fo)])
        except Exception:
            pass

        final = CompositeVideoClip([bg_clip, text_clip], size=(W, H)).with_duration(duration).with_fps(fps)

        # Audio frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed).subclipped(0, duration)
                final = final.with_audio(audio_clip)
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio simple discovery frasi importanti: {e}")

        return final
    except Exception as e:
        logger.error(f"Error creating simple discovery phrase overlay: {e}")
        logger.error(traceback.format_exc())
        return None


        def render_text_partial(num_chars):
            """Renderizza il testo fino a num_chars caratteri."""
            img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * 1.3)
            
            # Calcola larghezze
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            total_h = line_height * len(lines)
            base_y = (H - total_h) // 2
            
            chars_drawn = 0
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = (W - line_w) // 2
                y = base_y + i * line_height
                
                x = base_x
                for ch in line:
                    if chars_drawn >= num_chars:
                        return img
                    draw.text((x, y), ch, font=font, fill=text_color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                    chars_drawn += 1
            
            return img
        
        frames = []
        for n in range(total_chars + 1):
            frame_img = render_text_partial(n)
            frames.append(np.array(frame_img))
        
        def make_frame(t):
            """Frame con typewriter e cursore rosso."""
            progress = min(max(t / typewriter_duration, 0.0), 1.0) if typewriter_duration > 0 else 1.0
            idx = int(progress * total_chars)
            
            frame = frames[min(idx, len(frames) - 1)].copy()
            
            # Aggiungi cursore rosso se stiamo ancora scrivendo
            if idx < total_chars:
                blink_cycle = (t * 2.0) % 2.0  # Lampeggio veloce
                if blink_cycle < 1.0:  # Visibile
                    cursor_img = Image.fromarray(frame)
                    draw = ImageDraw.Draw(cursor_img)
                    
                    # Calcola posizione cursore (considerando virgolette)
                    lines_list = lines  # Usa lines invece di wrapped_text.split
                    base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
                    line_height = int(base_line_height * 2.2)  # Stesso line_height di render_text_partial
                    
                    # Calcola larghezze del testo
                    line_widths = []
                    for line in lines_list:
                        x = 0
                        for ch in line:
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                        line_widths.append(x)
                    
                    # Calcola larghezza delle virgolette
                    quote_bbox = quote_font.getbbox(quote_start)
                    quote_width = quote_bbox[2] - quote_bbox[0]
                    
                    # Larghezza totale
                    max_line_width = max(line_widths) if line_widths else 0
                    total_text_width = max_line_width + (quote_width * 2) + (tracking_px * 2)
                    
                    total_h = line_height * len(lines_list)
                    base_y = (H - total_h) // 2
                    
                    chars_counted = 0
                    cursor_x = 0
                    cursor_y = 0
                    
                    # Stessa logica di render_text_partial per la posizione della virgoletta
                    quote_start_x = (W - total_text_width) // 2 - int(quote_width * 0.5)  # Più distaccata a sinistra
                    text_start_x = quote_start_x + quote_width + tracking_px
                    
                    for i, line in enumerate(lines_list):
                        line_w = line_widths[i]
                        base_x = text_start_x  # Inizia dopo la virgoletta iniziale
                        y = base_y + i * line_height
                        
                        x = base_x
                        for ch in line:
                            if chars_counted == idx:
                                cursor_x = x
                                cursor_y = y
                                break
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                            chars_counted += 1
                        if chars_counted == idx:
                            break
                    
                    # Disegna cursore rosso
                    cursor_height = int(font_size * 1.1)
                    cursor_width = 12
                    
                    # Bordo nero per contrasto
                    border_rect = [
                        cursor_x - cursor_width // 2 - 2,
                        cursor_y - 3,
                        cursor_x + cursor_width // 2 + 2,
                        cursor_y + cursor_height + 3
                    ]
                    draw.rectangle(border_rect, fill=(0, 0, 0, 200))
                    
                    # Cursore rosso
                    cursor_rect = [
                        cursor_x - cursor_width // 2,
                        cursor_y,
                        cursor_x + cursor_width // 2,
                        cursor_y + cursor_height
                    ]
                    draw.rectangle(cursor_rect, fill=cursor_color)
                    
                    frame = np.array(cursor_img)
            
            return frame
        
        typewriter_clip = VideoClip(make_frame, duration=duration)
        typewriter_clip = typewriter_clip.with_position("center").with_start(0)
        
        # Background
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception:
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        else:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        final = CompositeVideoClip([bg_clip, typewriter_clip], size=(W, H)).with_duration(duration)
        
        # Aggiungi audio per frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, duration)
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per crime typewriter: {audio_path}")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio crime typewriter: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating crime typewriter overlay: {e}")
        logger.error(traceback.format_exc())
        return None
        
        # Use Montserrat Bold
        font = load_font_safe(font_path, font_size, bold=True)
        
        # Calculate character positions
        char_infos = calculate_char_positions(
            text, font, tracking=5, max_chars_per_line=25
        )
        
        # Load background frame
        bg_frame = None
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_frame = bg_clip.get_frame(0) if bg_clip.duration > 0 else None
            except Exception:
                pass
        
        # Create frame factory
        if effect_type == "scramble":
            def make_frame(t):
                return make_scramble_frame(t, char_infos, font, bg_frame)
        else:
            def make_frame(t):
                return make_permutation_frame(t, char_infos, font, bg_frame)
        
        overlay = VideoClip(make_frame, duration=duration)
        
        # Composite with background
        if bg_frame is not None:
            bg_clip = VideoFileClip(background_path).without_audio()
            bg_clip = bg_clip.resized((W, H))
            bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            final = CompositeVideoClip([bg_clip, overlay], size=(W, H)).with_duration(duration)
        else:
            bg = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
            final = CompositeVideoClip([bg, overlay], size=(W, H)).with_duration(duration)
        
        # Aggiungi audio per frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                
                # Taglia l'audio alla durata del video
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    # Se l'audio è più corto, loopalo
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, duration)
                
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per frasi importanti: {audio_path}, durata: {duration}s")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio frasi importanti: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating cinematic text overlay: {e}")
        logger.error(traceback.format_exc())
        return None
def create_rect_title_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 72,
    rect_color: Tuple[int, int, int] = (255, 0, 0),
    video_style: str = "discovery"  # "discovery", "crime", "rap"
) -> Optional[CompositeVideoClip]:
    """
    DISABLED: This function has been disabled. creative_rect_titles.py has been removed.
    Returns None to prevent any use of the red rectangle animation.
    """
    logger.warning("create_rect_title_overlay is disabled - creative_rect_titles.py has been removed")
    return None


def create_modern_quote_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    author: Optional[str] = None
) -> Optional[CompositeVideoClip]:
    """
    Creates a modern quote overlay (Rap style for important phrases).
    Based on modern_quotes_generator.py
    Uses Montserrat Black font and supports background video.
    """
    try:
        W, H = size
        
        # NOTE: Older versions depended on external `modern_quotes_generator.py`.
        # Current implementation is self-contained; keep running even if effects dir is missing.
        
        # Use FONT_PATH_DEFAULT instead of Montserrat Black
        try:
            from config.config import FONT_PATH_DEFAULT
        except ImportError:
            try:
                from config import FONT_PATH_DEFAULT
            except ImportError:
                FONT_PATH_DEFAULT = None
        
        # Use font_path parameter if provided, otherwise use FONT_PATH_DEFAULT
        default_font_path = font_path if font_path and os.path.exists(font_path) else (FONT_PATH_DEFAULT if FONT_PATH_DEFAULT and os.path.exists(FONT_PATH_DEFAULT) else None)
        
        # Find Montserrat font (same as test)
        def _find_montserrat_font_path():
            if default_font_path and "montserrat" in os.path.basename(default_font_path).lower():
                return default_font_path
            try:
                from config.config import FONT_PATHS, FONTS_DIR
            except ImportError:
                try:
                    from config import FONT_PATHS, FONTS_DIR
                except ImportError:
                    FONT_PATHS = []
                    FONTS_DIR = None
            for path in FONT_PATHS or []:
                if path and os.path.exists(path) and "montserrat" in os.path.basename(path).lower():
                    return path
            if FONTS_DIR and os.path.isdir(FONTS_DIR):
                for root, _, files in os.walk(FONTS_DIR):
                    for fname in files:
                        lower = fname.lower()
                        if "montserrat" in lower and (lower.endswith(".ttf") or lower.endswith(".otf")):
                            return os.path.join(root, fname)
            return default_font_path
        
        # Import PIL for image creation
        from PIL import Image, ImageDraw, ImageFont
        
        # Wrap text every 35 characters
        wrapped_text = wrap_text_every_n_chars(text, n=35)
        logger.debug(f"Text wrapped every 35 characters: {wrapped_text}")
        
        # Count number of lines after wrapping
        lines = wrapped_text.split('\n')
        num_lines = len(lines)
        logger.debug(f"Number of lines after wrapping: {num_lines}")
        
        # If more than 5 lines, reduce font size
        font_size = 70
        scale_reduction = 1.0
        if num_lines > 5:
            if num_lines >= 10:
                scale_reduction = 0.5
            elif num_lines >= 9:
                scale_reduction = 0.55
            elif num_lines >= 8:
                scale_reduction = 0.6
            elif num_lines >= 7:
                scale_reduction = 0.65
            elif num_lines >= 6:
                scale_reduction = 0.75
            else:
                scale_reduction = 0.8
            logger.debug(f"More than 5 lines ({num_lines}), applying scale reduction: {scale_reduction}x")
        
        font_size_scaled = int(font_size * scale_reduction)
        
        # Load font (same as test)
        montserrat_path = _find_montserrat_font_path()
        try:
            if montserrat_path:
                font = ImageFont.truetype(montserrat_path, font_size_scaled)
            else:
                font = ImageFont.load_default()
        except Exception:
            font = ImageFont.load_default()
        
        # Calculate text dimensions for each line (same as test)
        img_temp = Image.new("RGBA", (1, 1))
        draw_temp = ImageDraw.Draw(img_temp)
        
        line_dims = []
        for line in lines:
            bbox = draw_temp.textbbox((0, 0), line, font=font)
            text_w = bbox[2] - bbox[0]
            text_h = bbox[3] - bbox[1]
            padding_x = 40
            padding_y = 20
            box_w = text_w + 2 * padding_x
            box_h = text_h + 2 * padding_y
            line_dims.append({
                'text': line,
                'text_w': text_w,
                'text_h': text_h,
                'box_w': box_w,
                'box_h': box_h,
                'left': bbox[0],
                'top': bbox[1]
            })
        
        # Calculate initial positions (same as test)
        total_height = sum(d['box_h'] for d in line_dims)
        if num_lines > 5:
            top_margin = 100
            y_start = top_margin
        else:
            y_start = (H - total_height) // 2
        
        # Store rectangle positions (same as test)
        rectangles = []
        y_current = y_start
        
        for dim in line_dims:
            x_center = (W - dim['box_w']) // 2
            rectangles.append({
                'x_min': x_center,
                'x_max': x_center + dim['box_w'],
                'y_min': y_current,
                'y_max': y_current + dim['box_h'],
                'height': dim['box_h'],
                'width': dim['box_w'],
                'text': dim['text'],
                'text_w': dim['text_w'],
                'text_h': dim['text_h'],
                'text_left': dim['left'],
                'text_top': dim['top']
            })
            y_current += dim['box_h']
        
        # Apply collision detection (EXACT SAME AS TEST) - calculate once before frame function
        rectangles_sorted = sorted(rectangles, key=lambda r: r['y_min'])
        adjusted_rectangles = []
        min_spacing = 50  # 50px minimum spacing
        
        for rect in rectangles_sorted:
            new_y_min = rect['y_min']
            
            # Check overlap with all previously positioned rectangles
            for prev_rect in adjusted_rectangles:
                # Check if rectangles overlap horizontally
                horizontal_overlap = not (rect['x_max'] < prev_rect['x_min'] or rect['x_min'] > prev_rect['x_max'])
                if horizontal_overlap:
                    # They overlap horizontally, ensure minimum spacing
                    if new_y_min < prev_rect['y_max'] + min_spacing:
                        new_y_min = prev_rect['y_max'] + min_spacing
            
            # Update rectangle position
            rect['y_min'] = new_y_min
            rect['y_max'] = new_y_min + rect['height']
            adjusted_rectangles.append(rect)
        
        # Create frame function using test logic (works correctly)
        def make_frame_red_rectangles(t):
            """Create frame with red rectangles and white text (same logic as test)."""
            img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            # Draw rectangles and text (same as test)
            for rect in adjusted_rectangles:
                # Draw red rectangle (less bright red for Rap style)
                draw.rectangle(
                    [rect['x_min'], rect['y_min'], rect['x_max'], rect['y_max']],
                    fill=(180, 0, 0, 255)  # Darker red (less bright)
                )
                
                # Draw white text
                text_x = rect['x_min'] + (rect['width'] - rect['text_w']) // 2 - rect['text_left']
                text_y = rect['y_min'] + (rect['height'] - rect['text_h']) // 2 - rect['text_top']
                draw.text((text_x, text_y), rect['text'], font=font, fill=(255, 255, 255, 255))
            
            return np.array(img)
        
        final_duration = duration
        
        # Create overlay clip with transparent background
        overlay = VideoClip(make_frame_red_rectangles, duration=final_duration).with_fps(fps)
        
        # Background - use provided background or transparent
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(final_duration, bg_clip.duration)).with_duration(final_duration)
                # Aumenta la qualità del rendering
                bg_clip = bg_clip.with_fps(fps)  # Assicura fps corretto
            except Exception as e:
                logger.warning(f"Could not load background: {e}")
                # Use transparent background instead of black
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0, 0)).with_duration(final_duration).with_fps(fps)
        else:
            # Use transparent background instead of black
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0, 0)).with_duration(final_duration).with_fps(fps)
        
        # Aumenta la qualità del rendering del composite
        final = CompositeVideoClip([bg_clip, overlay], size=(W, H)).with_duration(final_duration).with_fps(fps)
        
        # Add light fade out for Rap style (frasi importanti)
        fade_out_duration = 0.5  # Light fade out duration
        if final_duration > fade_out_duration:
            final = final.with_effects([vfx.FadeOut(fade_out_duration)])
        
        # Aggiungi audio per frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > final_duration:
                    audio_clip = audio_clip.subclipped(0, final_duration)
                else:
                    loops_needed = int(final_duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, final_duration)
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per modern quote: {audio_path}")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio modern quote: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating modern quote overlay: {e}")
        logger.error(traceback.format_exc())
        return None


def create_crime_blur_zoom_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 100,
    text_color: Tuple[int, int, int] = (255, 255, 255)
) -> Optional[CompositeVideoClip]:
    """
    Creates a crime-style overlay for special names (Nomi_Speciali).
    Features:
    - Blur in entrata (fade in con blur che si riduce)
    - Leggero zoom in durante l'entrata
    """
    try:
        W, H = size
        
        # Use Montserrat Black for Crime style
        font = load_font_safe(font_path, font_size, black=True)
        
        # Render text image
        from PIL import Image, ImageDraw, ImageFont
        import numpy as np
        
        # Wrap text ogni 20 caratteri
        max_chars_per_line = 20
        wrapped_text = wrap_text_every_n_chars(text, max_chars_per_line)
        lines = wrapped_text.split("\n")
        
        # Calcola dimensioni del testo con wrap
        test_img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
        draw_test = ImageDraw.Draw(test_img)
        base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
        line_height = int(base_line_height * 1.3)
        
        # Calcola larghezze per ogni linea
        line_widths = []
        for line in lines:
            bbox = draw_test.textbbox((0, 0), line, font=font)
            line_widths.append(bbox[2] - bbox[0])
        
        max_line_width = max(line_widths) if line_widths else 0
        total_text_height = line_height * len(lines)
        
        # Crea immagine del testo con ombra e wrap
        text_img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
        draw = ImageDraw.Draw(text_img)
        
        base_x = (W - max_line_width) // 2
        base_y = (H - total_text_height) // 2
        
        # Ombra dietro il testo (nero semi-trasparente, leggermente spostata)
        shadow_offset = (3, 3)
        shadow_color = (0, 0, 0, 180)  # Nero con alpha 180/255
        
        for i, line in enumerate(lines):
            y = base_y + i * line_height
            line_w = line_widths[i]
            x = base_x + (max_line_width - line_w) // 2  # Centra ogni linea
            
            # Ombra
            draw.text((x + shadow_offset[0], y + shadow_offset[1]), line, font=font, fill=shadow_color)
            # Testo principale sopra l'ombra
            draw.text((x, y), line, font=font, fill=text_color)
        
        # Converti in array numpy
        text_array = np.array(text_img)
        
        # Durata dell'animazione di entrata (blur + zoom)
        entrance_duration = min(1.5, duration * 0.3)  # 30% della durata o max 1.5s
        
        def make_frame(t):
            """Frame con blur progressivo e zoom in."""
            frame = text_array.copy()
            
            if t < entrance_duration:
                # Fase di entrata: blur che si riduce + zoom in
                progress = t / entrance_duration  # 0.0 -> 1.0
                
                # Blur: da massimo a zero
                max_blur = 20
                current_blur = max_blur * (1.0 - progress)
                
                # Zoom: da 0.9 a 1.0 (leggero zoom in)
                zoom_start = 0.9
                zoom_end = 1.0
                current_zoom = zoom_start + (zoom_end - zoom_start) * progress
                
                # Applica blur
                if current_blur > 0.1:
                    pil_frame = Image.fromarray(frame)
                    blurred = pil_frame.filter(ImageFilter.GaussianBlur(radius=current_blur))
                    frame = np.array(blurred)
                
                # Applica zoom (resize e crop)
                if current_zoom < 1.0:
                    zoom_w = int(W * current_zoom)
                    zoom_h = int(H * current_zoom)
                    pil_frame = Image.fromarray(frame)
                    zoomed = pil_frame.resize((zoom_w, zoom_h), Image.LANCZOS)
                    # Ricentra
                    result = Image.new("RGBA", (W, H), (0, 0, 0, 0))
                    offset_x = (W - zoom_w) // 2
                    offset_y = (H - zoom_h) // 2
                    result.paste(zoomed, (offset_x, offset_y))
                    frame = np.array(result)
                
                # Fade in (opacità)
                alpha = progress
                frame[:, :, 3] = (frame[:, :, 3] * alpha).astype(np.uint8)
            
            return frame
        
        overlay = VideoClip(make_frame, duration=duration)
        
        # Background
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception:
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        else:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        final = CompositeVideoClip([bg_clip, overlay], size=(W, H)).with_duration(duration)
        
        # Aggiungi audio per parole importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "parole_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, duration)
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per crime blur zoom: {audio_path}")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio crime blur zoom: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating crime blur zoom overlay: {e}")
        import traceback
        logger.error(traceback.format_exc())
        return None


def create_crime_typewriter_overlay(
    text: str,
    duration: float,
    size: Tuple[int, int],
    fps: float,
    background_path: Optional[str] = None,
    font_path: Optional[str] = None,
    font_size: int = 80,
    text_color: Tuple[int, int, int] = (255, 255, 255),
    cursor_color: Tuple[int, int, int] = (255, 0, 0)  # Cursore rosso
) -> Optional[CompositeVideoClip]:
    """
    Creates a crime-style overlay for important phrases (Frasi_Importanti).
    Features:
    - Typewriter effect (scrittura lettera per lettera)
    - Cursore rosso lampeggiante
    """
    try:
        W, H = size
        
        # Use FONT_PATH_DEFAULT instead of Montserrat Black
        try:
            from config.config import FONT_PATH_DEFAULT
        except ImportError:
            try:
                from config import FONT_PATH_DEFAULT
            except ImportError:
                FONT_PATH_DEFAULT = None
        
        # Use font_path parameter if provided, otherwise use FONT_PATH_DEFAULT
        default_font_path = font_path if font_path and os.path.exists(font_path) else (FONT_PATH_DEFAULT if FONT_PATH_DEFAULT and os.path.exists(FONT_PATH_DEFAULT) else None)
        
        from PIL import Image, ImageDraw, ImageFont
        import numpy as np
        
        if default_font_path:
            try:
                font = ImageFont.truetype(default_font_path, font_size)
            except Exception:
                font = load_font_safe(font_path, font_size, black=True)  # Fallback
        else:
            font = load_font_safe(font_path, font_size, black=True)  # Fallback
        
        # Wrap text ogni 25 caratteri
        max_chars_per_line = 25
        words = text.split()
        lines = []
        current_line = ""
        
        for word in words:
            if len(current_line) + len(word) + 1 <= max_chars_per_line:
                current_line += (" " if current_line else "") + word
            else:
                if current_line:
                    lines.append(current_line)
                current_line = word
        if current_line:
            lines.append(current_line)
        
        # Le virgolette vengono sempre disegnate separatamente come elementi grafici
        # Non aggiungiamo virgolette al testo stesso per evitare duplicati
        quote_start = '"'
        quote_end = '"'
        wrapped_text = "\n".join(lines)
        
        # Calcola tracking
        tracking_px = int(font_size * 0.1)
        
        # Durata typewriter
        total_chars = sum(len(line) for line in lines)
        typewriter_duration = min(3.0, duration * 0.6)
        char_time = typewriter_duration / max(total_chars, 1)
        
        # Font per virgolette (molto più grande) - usa stesso font di default
        quote_font_size = int(font_size * 2.0)  # 100% più grande (2x)
        if default_font_path:
            try:
                quote_font = ImageFont.truetype(default_font_path, quote_font_size)
            except Exception:
                quote_font = load_font_safe(font_path, quote_font_size, black=True)  # Fallback
        else:
            quote_font = load_font_safe(font_path, quote_font_size, black=True)  # Fallback
        
        # Render frames per typewriter
        def render_text_partial(num_chars):
            """Renderizza il testo fino a num_chars caratteri con virgolette rosse."""
            img = Image.new("RGBA", (W, H), (0, 0, 0, 0))
            draw = ImageDraw.Draw(img)
            
            base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
            line_height = int(base_line_height * 2.2)  # Ancora più spazio tra le righe (da 1.8 a 2.2)
            
            # Calcola larghezze del testo (senza virgolette)
            line_widths = []
            for line in lines:
                x = 0
                for ch in line:
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                line_widths.append(x)
            
            # Calcola larghezza delle virgolette
            quote_bbox = quote_font.getbbox(quote_start)
            quote_width = quote_bbox[2] - quote_bbox[0]
            
            # Larghezza totale (testo + virgolette)
            max_line_width = max(line_widths) if line_widths else 0
            total_text_width = max_line_width + (quote_width * 2) + (tracking_px * 2)
            
            total_h = line_height * len(lines)
            # Centra verticalmente per Crime style (come Rap)
            # Centra meglio verticalmente, soprattutto per le ultime frasi
            base_y = (H - total_h) // 2  # Centrato verticalmente
            
            # Disegna virgolette rosse all'inizio e alla fine (sempre visibili)
            # Virgolette più distanti in alto e in basso
            quote_y_start = base_y - int(line_height * 0.7)  # Ancora più in alto (da 0.5 a 0.7)
            quote_y_end = base_y + (len(lines) - 1) * line_height + int(line_height * 0.5)  # Più in basso
            # Centra meglio orizzontalmente con più padding dai bordi
            quote_start_x = (W - total_text_width) // 2  # Centrato perfettamente
            quote_end_x = quote_start_x + max_line_width + quote_width + tracking_px
            
            # Virgolette rosse
            draw.text((quote_start_x, quote_y_start), quote_start, font=quote_font, fill=(255, 0, 0))
            draw.text((quote_end_x, quote_y_end), quote_end, font=quote_font, fill=(255, 0, 0))
            
            # Disegna il testo con ombra
            chars_drawn = 0
            text_start_x = quote_start_x + quote_width + tracking_px
            shadow_offset = (4, 4)  # Offset ombra più pronunciato
            
            for i, line in enumerate(lines):
                line_w = line_widths[i]
                base_x = text_start_x  # Inizia dopo la virgoletta iniziale
                y = base_y + i * line_height
                
                x = base_x
                for ch in line:
                    if chars_drawn >= num_chars:
                        return img
                    # Disegna ombra prima del testo
                    draw.text((x + shadow_offset[0], y + shadow_offset[1]), ch, font=font, fill=(0, 0, 0, 220))  # Ombra nera
                    # Disegna testo principale sopra l'ombra
                    draw.text((x, y), ch, font=font, fill=text_color)
                    bbox = font.getbbox(ch)
                    ch_w = bbox[2] - bbox[0]
                    x += ch_w + tracking_px
                    chars_drawn += 1
            
            return img
        
        frames = []
        for n in range(total_chars + 1):
            frame_img = render_text_partial(n)
            frames.append(np.array(frame_img))
        
        def make_frame(t):
            """Frame con typewriter e cursore rosso."""
            progress = min(max(t / typewriter_duration, 0.0), 1.0) if typewriter_duration > 0 else 1.0
            idx = int(progress * total_chars)
            
            frame = frames[min(idx, len(frames) - 1)].copy()
            
            # Aggiungi cursore rosso se stiamo ancora scrivendo
            if idx < total_chars:
                blink_cycle = (t * 2.0) % 2.0  # Lampeggio veloce
                if blink_cycle < 1.0:  # Visibile
                    cursor_img = Image.fromarray(frame)
                    draw = ImageDraw.Draw(cursor_img)
                    
                    # Calcola posizione cursore (considerando virgolette)
                    lines_list = lines  # Usa lines invece di wrapped_text.split
                    base_line_height = font.getbbox("Ay")[3] - font.getbbox("Ay")[1]
                    line_height = int(base_line_height * 2.2)  # Stesso line_height di render_text_partial
                    
                    # Calcola larghezze del testo
                    line_widths = []
                    for line in lines_list:
                        x = 0
                        for ch in line:
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                        line_widths.append(x)
                    
                    # Calcola larghezza delle virgolette
                    quote_bbox = quote_font.getbbox(quote_start)
                    quote_width = quote_bbox[2] - quote_bbox[0]
                    
                    # Larghezza totale
                    max_line_width = max(line_widths) if line_widths else 0
                    total_text_width = max_line_width + (quote_width * 2) + (tracking_px * 2)
                    
                    total_h = line_height * len(lines_list)
                    base_y = (H - total_h) // 2
                    
                    chars_counted = 0
                    cursor_x = 0
                    cursor_y = 0
                    
                    # Stessa logica di render_text_partial per la posizione della virgoletta
                    quote_start_x = (W - total_text_width) // 2 - int(quote_width * 0.5)  # Più distaccata a sinistra
                    text_start_x = quote_start_x + quote_width + tracking_px
                    
                    for i, line in enumerate(lines_list):
                        line_w = line_widths[i]
                        base_x = text_start_x  # Inizia dopo la virgoletta iniziale
                        y = base_y + i * line_height
                        
                        x = base_x
                        for ch in line:
                            if chars_counted == idx:
                                cursor_x = x
                                cursor_y = y
                                break
                            bbox = font.getbbox(ch)
                            ch_w = bbox[2] - bbox[0]
                            x += ch_w + tracking_px
                            chars_counted += 1
                        if chars_counted == idx:
                            break
                    
                    # Disegna cursore rosso
                    cursor_height = int(font_size * 1.1)
                    cursor_width = 12
                    
                    # Bordo nero per contrasto
                    border_rect = [
                        cursor_x - cursor_width // 2 - 2,
                        cursor_y - 3,
                        cursor_x + cursor_width // 2 + 2,
                        cursor_y + cursor_height + 3
                    ]
                    draw.rectangle(border_rect, fill=(0, 0, 0, 200))
                    
                    # Cursore rosso
                    cursor_rect = [
                        cursor_x - cursor_width // 2,
                        cursor_y,
                        cursor_x + cursor_width // 2,
                        cursor_y + cursor_height
                    ]
                    draw.rectangle(cursor_rect, fill=cursor_color)
                    
                    frame = np.array(cursor_img)
            
            return frame
        
        typewriter_clip = VideoClip(make_frame, duration=duration)
        typewriter_clip = typewriter_clip.with_position("center").with_start(0)
        
        # Background
        if background_path and os.path.exists(background_path):
            try:
                bg_clip = VideoFileClip(background_path).without_audio()
                bg_clip = bg_clip.resized((W, H))
                bg_clip = bg_clip.subclipped(0, min(duration, bg_clip.duration)).with_duration(duration)
            except Exception:
                bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        else:
            bg_clip = ColorClip(size=(W, H), color=(0, 0, 0)).with_duration(duration)
        
        final = CompositeVideoClip([bg_clip, typewriter_clip], size=(W, H)).with_duration(duration)
        
        # Aggiungi audio per frasi importanti
        audio_path = os.path.join(os.path.dirname(__file__), "assets", "audio", "frasi_importanti.mp3")
        if os.path.exists(audio_path) and AudioFileClip is not None:
            try:
                audio_clip = AudioFileClip(audio_path)
                if audio_clip.duration > duration:
                    audio_clip = audio_clip.subclipped(0, duration)
                else:
                    loops_needed = int(duration / audio_clip.duration) + 1
                    if concatenate_audioclips:
                        audio_clip = concatenate_audioclips([audio_clip] * loops_needed)
                        audio_clip = audio_clip.subclipped(0, duration)
                final = final.with_audio(audio_clip)
                logger.debug(f"Audio aggiunto per crime typewriter: {audio_path}")
            except Exception as e:
                logger.warning(f"Errore nell'aggiungere audio crime typewriter: {e}")
        
        return final
        
    except Exception as e:
        logger.error(f"Error creating crime typewriter overlay: {e}")
        import traceback
        logger.error(traceback.format_exc())
        return None
