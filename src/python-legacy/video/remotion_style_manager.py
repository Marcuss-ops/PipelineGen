"""
Remotion Style Manager - Gestisce la selezione random degli stili Remotion per le animazioni.

Questo modulo seleziona gli stili all'inizio del video e li mantiene consistenti per tutto il video.
"""

import random
from collections import deque
import logging
from typing import Dict, List, Optional, Tuple
from dataclasses import dataclass

logger = logging.getLogger(__name__)

# ============================================================================
# DEFINIZIONI STILI (da AVAILABLE_STYLES.md)
# ============================================================================

# FRASI_IMPORTANTI
FRASI_IMPORTANTI_CLEAN = [
    "Clean-01-MaskReveal",
    "Clean-02-CursorReveal",
    "Clean-03-UnderscoreSweep",
    "Clean-04-CaretJump",
    "Clean-06-WeightShift",
    "Clean-07-LivingCursor",
    "Clean-09-UnderlineDraw",
    "Clean-18-BlinkTyping",
    "Clean-19-TypewriterHuman",
    "Clean-20-TypewriterErase",
    "Clean-21-TypewriterGhost",
    "Clean-23-TypewriterAccel",
    "Clean-24-FadeUpLines",
    "Clean-25-SlideInLines",
    "Clean-26-BlurRevealLines",
    "Clean-27-ScaleUpLines",
    "Clean-28-TypewriterClassic",
    "Clean-29-TypewriterEmotional",
    "Clean-30-TypewriterNoCursor",
    "Clean-31-TypewriterFixedCursor",
    "Clean-32-TypewriterDelayStart",
    "Clean-33-TypewriterThinking",
    "Clean-34-MinimalFadeWords",
    "Clean-39-MinimalTrackingFocus"
]

FRASI_IMPORTANTI_MINIMALIST = [
    "Minimal-SoftScramble",
    "Minimal-TypingSlow",
    "Minimal-PartialScramble",
    "Minimal-FadeUpWords",
    "Minimal-SlideSoft",
    "Minimal-OpacityWave",
    "Minimal-UnderlineFollow",
    "Minimal-TextFocus",
    "Minimal-ColorEmphasis",
    "Minimal-BlurSlide",
    "Minimal-TrackingFocus",
    "Minimal-GlowPulse",
    "Minimal-Atmospheric",
]

FRASI_IMPORTANTI_COMPLEXBOX = [
    "Complex-Box-Min-SoftScramble",
    "Complex-Box-Min-TypingSlow",
    "Complex-Box-Min-TextFocus",
    "Complex-Box-Min-TrackingFocus",
    "Complex-Box-Min-SlideSoft",
    "Complex-Box-Min-BlurSlide",
    "Complex-Box-Min-GlowPulse",
    "Complex-Box-Min-CursorBlock",
    "Complex-Box-Min-CursorUnderscore",
]

FRASI_IMPORTANTI_BLURZOOM = [
    "BlurZoom-Cut",
    "BlurZoom-Fade",
]

FRASI_IMPORTANTI_ALL = (
    FRASI_IMPORTANTI_CLEAN +
    FRASI_IMPORTANTI_MINIMALIST +
    FRASI_IMPORTANTI_COMPLEXBOX +
    FRASI_IMPORTANTI_BLURZOOM
)

# NOMI_SPECIALI
NOMI_SPECIALI_BASE = [
    "NomiSpeciali-Base",
]

NOMI_SPECIALI_TYPEWRITER = [
    "NomiSpeciali-Typewriter-V1-Classic",
    "NomiSpeciali-Typewriter-V2-Cinematic",
    "NomiSpeciali-Typewriter-V4-CursorDriven",
    "NomiSpeciali-Typewriter-V5-Breath",
    "NomiSpeciali-Typewriter-V7-FadeOnly",
    "NomiSpeciali-Typewriter-V8-Ghost",
    "NomiSpeciali-Typewriter-V9-Delayed",
    "NomiSpeciali-Typewriter-V10-Terminal",
    "NomiSpeciali-Typewriter-V11-Decoding",
    "NomiSpeciali-Typewriter-V15-Redaction",
    "NomiSpeciali-Typewriter-V16-ColorShift",
]

NOMI_SPECIALI_HIGHLIGHTER = [
    "NomiSpeciali-Highlight-V1-DoubleRed",
    "NomiSpeciali-Highlight-V2-Marker",
    "NomiSpeciali-Highlight-V5-Underline",
]

NOMI_SPECIALI_CLASSIC = [
    "NomiSpeciali-Classic-V2-SlideSoft",
    "NomiSpeciali-Classic-V3-TrackingFocus",
    "NomiSpeciali-Classic-V4-BlurSlide",
    "NomiSpeciali-Classic-V5-Atmospheric",
    "NomiSpeciali-Classic-V7-GlowPulse",
]

NOMI_SPECIALI_WHITE = [
    "NomiSpeciali-Typewriter-V1-White",
    "NomiSpeciali-Classic-V2-White",
    "NomiSpeciali-Highlight-V2-White",
]

NOMI_SPECIALI_ALL = (
    NOMI_SPECIALI_BASE +
    NOMI_SPECIALI_TYPEWRITER +
    NOMI_SPECIALI_HIGHLIGHTER +
    NOMI_SPECIALI_CLASSIC +
    NOMI_SPECIALI_WHITE
)

# DATE
DATE_TYPEWRITER = [
    "Clean-19-TypewriterHuman",
    "Clean-20-TypewriterErase",
    "Clean-21-TypewriterGhost",
    "Clean-23-TypewriterAccel",
    "Clean-28-TypewriterClassic",
    "Clean-29-TypewriterEmotional",
    "Clean-30-TypewriterNoCursor",
    "Clean-31-TypewriterFixedCursor",
    "Clean-32-TypewriterDelayStart",
    "Clean-33-TypewriterThinking",
    "NomiSpeciali-Typewriter-V1-Classic",
    "NomiSpeciali-Typewriter-V10-Terminal",
]

DATE_CLEAN = [
    "Clean-01-MaskReveal",
    "Clean-02-CursorReveal",
    "Clean-03-UnderscoreSweep",
    "Clean-04-CaretJump",
    "Clean-24-FadeUpLines",
    "Clean-25-SlideInLines",
    "Clean-26-BlurRevealLines",
    "Clean-27-ScaleUpLines",
]

DATE_MINIMALIST = [
    "Minimal-SoftScramble",
    "Minimal-TypingSlow",
    "Minimal-FadeUpWords",
    "Minimal-SlideSoft",
    "Minimal-TextFocus",
]

DATE_ALL = DATE_TYPEWRITER + DATE_CLEAN + DATE_MINIMALIST

# NUMERI
NUMERI_3D = [
    "Numeric-3D-RotateY",
    "Numeric-3D-DepthZoom",
    "Numeric-3D-Swing",
]

NUMERI_TYPEWRITER = [
    "Numeric-TypeWriter",
    "Numeric-CursorRedGlow",
    "Numeric-FocusCursor",
]

NUMERI_SLIDE = [
    "Numeric-FadeSlide",
    "Numeric-SlideHorizontal",
    "Numeric-CountUp",
]

NUMERI_3DGLOW = [
    "Numeric-3DGlow-Typewriter",
    "Numeric-3DGlow-SlideLeft",
    "Numeric-3DGlow-ZoomIn",
    "Numeric-3DGlow-SlideUp",
    "Numeric-3DGlow-SlideDown",
    "Numeric-3DGlow-Defocus",
]

NUMERI_SPECIAL = [
    "Numeric-CrossBlur",
    "Numeric-Glass",
]

NUMERI_ALL = NUMERI_3D + NUMERI_TYPEWRITER + NUMERI_SLIDE + NUMERI_3DGLOW + NUMERI_SPECIAL

# IMMAGINI (da ImageAnimation.tsx)
IMMAGINI_ALL = [
    "Image-ZoomSlide",
    "Image-3D-Float-Var5",
    "Image-PullBack",
    "Image-Handheld",
    "Image-Pulse",
    "Image-Asymmetric",
    "Image-FocusReveal",
    "Image-Easy-ZoomIn",
    "Image-Easy-Pan",
    "Image-Easy-Tilt",
    "Image-Easy-Breath",
    "Image-Easy-Fade",
    "Image-Impact",
    "Image-Stutter",
    "Image-SnapZoom",
    "Image-DriftLoop",
    "Image-FlashReveal",
]


# ============================================================================
# CLASSE PER GESTIRE GLI STILI
# ============================================================================

@dataclass
class RemotionStyleSelection:
    """Contiene la selezione degli stili per un video"""
    # Frasi_Importanti: lista di 3 stili selezionati (Discovery) o più (Rap/Crime)
    frasi_importanti_pool: List[str]
    
    # Nomi_Speciali: lista di 2 stili selezionati
    nomi_speciali_pool: List[str]
    
    # Date: un solo stile per tutto il video
    date_style: str
    
    # Numeri: un solo stile per tutto il video
    numeri_style: str
    
    # Immagini: lista di 3 stili selezionati
    immagini_pool: List[str]
    
    video_style: str  # "discovery", "rap", "crime", "young"

    # Entity Stack Layer Style: un solo stile per tutto il video (DEFAULT o 4 direzionali)
    stack_layer_style: str  # "DEFAULT", "BOTTOM_TO_TOP", "LEFT_TO_RIGHT", "TOP_TO_BOTTOM"


class RemotionStyleManager:
    """Gestisce la selezione e il riutilizzo degli stili Remotion"""
    
    def __init__(self, video_style: str = "young", seed: Optional[int] = None):
        """
        Inizializza il manager degli stili.
        
        Args:
            video_style: Stile video ("discovery", "rap", "young", "crime")
            seed: Seed per random (opzionale, per riproducibilità)
        """
        self.video_style = video_style.lower()
        if self.video_style == "rap":
            self.video_style = "young"
        elif self.video_style == "old":
            self.video_style = "discovery"
        
        if seed is not None:
            random.seed(seed)
        
        # Seleziona gli stili all'inizio
        self.style_selection = self._select_styles()
        # Cicli per evitare ripetizioni immediate
        self._frasi_cycle = deque(random.sample(self.style_selection.frasi_importanti_pool, len(self.style_selection.frasi_importanti_pool))) if self.style_selection.frasi_importanti_pool else deque()
        self._nomi_cycle = deque(random.sample(self.style_selection.nomi_speciali_pool, len(self.style_selection.nomi_speciali_pool))) if self.style_selection.nomi_speciali_pool else deque()
        
        # Log della selezione
        self._log_selection()
    
    def _select_styles(self) -> RemotionStyleSelection:
        """Seleziona gli stili in base al video_style"""
        
        if self.video_style == "discovery":
            # Discovery: 3 random per Frasi_Importanti (solo Minimalist)
            frasi_pool = random.sample(FRASI_IMPORTANTI_MINIMALIST, min(3, len(FRASI_IMPORTANTI_MINIMALIST)))
            
            # 2 random per Nomi_Speciali (da tutte le categorie)
            nomi_candidates = (
                NOMI_SPECIALI_TYPEWRITER +
                NOMI_SPECIALI_HIGHLIGHTER +
                NOMI_SPECIALI_CLASSIC +
                NOMI_SPECIALI_WHITE
            )
            nomi_pool = random.sample(nomi_candidates, min(2, len(nomi_candidates)))
            
        elif self.video_style in ["rap", "young", "crime"]:
            # Rap/Crime: può scegliere anche altri stili per Frasi_Importanti
            # Seleziona 3 random da tutte le categorie
            frasi_pool = random.sample(FRASI_IMPORTANTI_ALL, min(3, len(FRASI_IMPORTANTI_ALL)))
            
            # 2 random per Nomi_Speciali (da tutte le categorie)
            nomi_candidates = (
                NOMI_SPECIALI_TYPEWRITER +
                NOMI_SPECIALI_HIGHLIGHTER +
                NOMI_SPECIALI_CLASSIC +
                NOMI_SPECIALI_WHITE
            )
            nomi_pool = random.sample(nomi_candidates, min(2, len(nomi_candidates)))
        else:
            # Default: usa tutte le opzioni
            frasi_pool = random.sample(FRASI_IMPORTANTI_ALL, min(3, len(FRASI_IMPORTANTI_ALL)))
            nomi_candidates = NOMI_SPECIALI_ALL
            nomi_pool = random.sample(nomi_candidates, min(2, len(nomi_candidates)))
        
        # Date: un solo stile random per tutto il video
        date_style = random.choice(DATE_ALL)
        
        # Numeri: un solo stile random per tutto il video
        numeri_style = random.choice(NUMERI_ALL)
        
        # Immagini: sempre 3 stili casuali (indipendentemente dallo stile)
        immagini_pool = random.sample(IMMAGINI_ALL, min(3, len(IMMAGINI_ALL)))

        # Entity Stack Layer Style: un solo stile per tutto il video (in caso ci siano entità multi-layer)
        stack_layer_styles = ["DEFAULT", "BOTTOM_TO_TOP", "LEFT_TO_RIGHT", "TOP_TO_BOTTOM"]
        stack_layer_style = random.choice(stack_layer_styles)

        return RemotionStyleSelection(
            frasi_importanti_pool=frasi_pool,
            nomi_speciali_pool=nomi_pool,
            date_style=date_style,
            numeri_style=numeri_style,
            immagini_pool=immagini_pool,
            video_style=self.video_style,
            stack_layer_style=stack_layer_style,
        )
    
    def _log_selection(self):
        """Log della selezione degli stili"""
        logger.info("=" * 70)
        logger.info("🎨 REMOTION STYLE SELECTION")
        logger.info("=" * 70)
        logger.info(f"   📋 Video Style: {self.video_style.upper()}")
        logger.info("")
        logger.info(f"   📝 Frasi_Importanti Pool ({len(self.style_selection.frasi_importanti_pool)} stili):")
        for i, style in enumerate(self.style_selection.frasi_importanti_pool, 1):
            logger.info(f"      {i}. {style}")
        logger.info("")
        logger.info(f"   🔤 Nomi_Speciali Pool ({len(self.style_selection.nomi_speciali_pool)} stili):")
        for i, style in enumerate(self.style_selection.nomi_speciali_pool, 1):
            logger.info(f"      {i}. {style}")
        logger.info("")
        logger.info(f"   📅 Date Style (fisso per tutto il video):")
        logger.info(f"      {self.style_selection.date_style}")
        logger.info("")
        logger.info(f"   🔢 Numeri Style (fisso per tutto il video):")
        logger.info(f"      {self.style_selection.numeri_style}")
        logger.info("")
        logger.info(f"   🖼️  Immagini Pool ({len(self.style_selection.immagini_pool)} stili):")
        for i, style in enumerate(self.style_selection.immagini_pool, 1):
            logger.info(f"      {i}. {style}")
        logger.info("")
        logger.info(f"   📚 Entity Stack Layer Style (fisso per tutto il video, in caso ci siano entità multi-layer):")
        logger.info(f"      {self.style_selection.stack_layer_style}")
        logger.info("=" * 70)
    
    def get_frasi_importanti_style(self) -> str:
        """Restituisce uno stile random dal pool di Frasi_Importanti"""
        if not self._frasi_cycle:
            return random.choice(self.style_selection.frasi_importanti_pool)
        style = self._frasi_cycle[0]
        self._frasi_cycle.rotate(-1)
        return style
    
    def get_nomi_speciali_style(self) -> str:
        """Restituisce uno stile random dal pool di Nomi_Speciali"""
        if not self._nomi_cycle:
            return random.choice(self.style_selection.nomi_speciali_pool)
        style = self._nomi_cycle[0]
        self._nomi_cycle.rotate(-1)
        return style
    
    def get_date_style(self) -> str:
        """Restituisce lo stile per Date (fisso per tutto il video)"""
        return self.style_selection.date_style
    
    def get_numeri_style(self) -> str:
        """Restituisce lo stile per Numeri (fisso per tutto il video)"""
        return self.style_selection.numeri_style
    
    def get_immagini_style(self) -> str:
        """Restituisce uno stile random dal pool di Immagini"""
        return random.choice(self.style_selection.immagini_pool)

    def get_stack_layer_style(self) -> str:
        """Restituisce lo stile Entity Stack Layer (fisso per tutto il video: DEFAULT o uno dei 4 direzionali)"""
        return self.style_selection.stack_layer_style


# ============================================================================
# ISTANZA GLOBALE (verrà inizializzata all'inizio del video)
# ============================================================================

_global_style_manager: Optional[RemotionStyleManager] = None


def initialize_style_manager(video_style: str, seed: Optional[int] = None) -> RemotionStyleManager:
    """
    Inizializza il manager degli stili globale.
    Deve essere chiamato all'inizio di generate_video_parallelized.
    """
    global _global_style_manager
    _global_style_manager = RemotionStyleManager(video_style, seed)
    return _global_style_manager


def get_style_manager() -> Optional[RemotionStyleManager]:
    """Restituisce il manager degli stili globale"""
    return _global_style_manager


def reset_style_manager():
    """Resetta il manager degli stili (per test)"""
    global _global_style_manager
    _global_style_manager = None
