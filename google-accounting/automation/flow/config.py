"""
Configurazioni e selettori per l'automazione Google Flow.

Tutti i selettori CSS/XPath sono centralizzati qui per facilitare debug e modifiche.
"""
import logging

# ── Stili immagine ─────────────────────────────────────────────────────────────
STYLE_MAP = {
    "realistic": "extremely detailed, realistic, 8k, photorealistic, cinematic lighting",
    "cartoon": "cartoon style, 2d, colorful, high quality animation, vibrant",
    "medieval": "medieval style, fantasy, historical, detailed oil painting, epic atmosphere",
    "cyberpunk": "cyberpunk aesthetic, neon lights, futuristic, dark atmosphere, high tech",
    "watercolor": "watercolor painting style, soft colors, artistic, fluid textures",
    "3d-render": "3d render, octane render, unreal engine 5 style, volumetric lighting, masterpiece",
    "sketch": "hand-drawn sketch, pencil drawing, monochrome, detailed lines, artistic",
    "cinematic": "cinematic lighting, movie shot, 35mm lens, highly detailed, dramatic",
}

# ── Selettori prompt textbox ───────────────────────────────────────────────────
# Ordine: dal più specifico al meno specifico
PROMPT_SELECTORS = [
    'div[role="textbox"]',           # Prompt input principale di Flow (AGENT panel)
    'div[contenteditable="true"]',   # Editor prompt area (fallback)
    'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[1]/div/p',  # Hardcoded fallback
]

# ── Selettori pulsante Genera/Crea ─────────────────────────────────────────────
# Questi pulsanti appaiono DOPO che l'Agente ha generato le immagini
GENERATE_BUTTON_SELECTORS = [
    'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[2]/div[2]/button[2]',
    'button:has-text("Crea")',
    'button:has-text("Generate")',
    'button[aria-label*="genera" i]',
]

# ── Selettori conteggio immagini ───────────────────────────────────────────────
IMAGE_COUNT_TOGGLE_SELECTOR = (
    'button[id^="radix-"]:has-text("x1"), '
    'button[id^="radix-"]:has-text("x2"), '
    'button[id^="radix-"]:has-text("x3"), '
    'button:has-text("x1"), '
    'button:has-text("x2"), '
    'button:has-text("x3")'
)
IMAGE_COUNT_FOUR_SELECTOR = (
    'button[id$="trigger-4"], '
    'button[role="tab"]:has-text("x4"), '
    'button:has-text("x4")'
)

# ── Selettori dashboard ────────────────────────────────────────────────────────
NEW_PROJECT_BUTTON_TEXTS = ["Nuovo progetto", "New project", "Nuovo", "New"]

# ── Limiti temporali ───────────────────────────────────────────────────────────
IMAGE_WAIT_TIMEOUT = 60           # secondi massimi attesa immagini
POST_GOTO_WAIT = 15               # attesa dopo navigazione (versione stabile)
POST_CLICK_WAIT = 3               # attesa dopo click Crea
POST_ENTER_WAIT = 3               # attesa dopo invio prompt
TYPE_DELAY_MS = 80                # delay tra caratteri durante digitazione
DOM_POLL_INTERVAL = 5             # secondi tra polling DOM

# ── Soglie contenuti ───────────────────────────────────────────────────────────
MIN_IMAGE_BODY_BYTES = 1000       # body minimo per network capture
MIN_DOM_IMAGE_BYTES = 2000        # body minimo per DOM capture
MAX_IMAGES = 4                    # numero massimo immagini da catturare

# ── URL pattern ────────────────────────────────────────────────────────────────
IMAGE_DOM_PATTERN = "googleusercontent.com"
MEDIA_REDIRECT_PATTERN = "media.getMediaUrlRedirect"

# ── Logger ─────────────────────────────────────────────────────────────────────
log = logging.getLogger("AutomationEngine.FlowConfig")