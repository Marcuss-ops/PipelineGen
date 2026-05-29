"""
Configurazioni e selettori per l'automazione Google Flow.

Tutti i selettori CSS/XPath sono centralizzati qui per facilitare debug e modifiche.
"""
import logging

from style_presets import STYLE_PRESETS

# ── Stili immagine ─────────────────────────────────────────────────────────────
STYLE_MAP = dict(STYLE_PRESETS)

# ── Selettori prompt textbox ───────────────────────────────────────────────────
# Ordine: dal più specifico al meno specifico
PROMPT_SELECTORS = [
    'div[data-slate-editor="true"]',  # Slate.js editor
    'xpath=//*[@id="__next"]/div[1]/div[5]/div/div/div/div/div[1]/div',
    'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[1]/div',
    'css=#__next > div.sc-c7ee1759-1.jhwuTJ > div.sc-cace806a-1.kMPsJV > div > div > div > div > div.sc-586bebe6-3.iHPpak > div',
    'div[role="textbox"]',
    'textarea',
]

# ── Selettori pulsante di invio (Genera) ──────────────────────────────────────
SEND_BUTTON_SELECTORS = [
    'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[2]/div[2]/button[2]',
    'xpath=//*[@id="__next"]/div[1]/div[5]/div/div/div/div/div[2]/div[2]/button[2]',
    'css=#__next > div.sc-c7ee1759-1.jhwuTJ > div.sc-cace806a-1.kMPsJV > div > div > div > div > div.sc-586bebe6-1.cfVQzc > div.sc-586bebe6-10.fChOpq > button.sc-e8425ea6-0.hOBPaw.sc-d3791a4f-0.sc-d3791a4f-4.sc-586bebe6-5.ewGlDn.famhRe.cJBRhk',
    'button:has-text("Crea"), button:has-text("Create"), button:has(i:has-text("arrow_forward"))'
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
NEW_PROJECT_BUTTON_TEXTS = ["Nuovo progetto", "New project", "Nuovo", "New", "Crea", "Create"]

# ── Selettori Agente / Approvazione ───────────────────────────────────────────
# Aggiornati con le ultime indicazioni dell'utente (proviamo varianti)
AGENT_TOGGLE_XPATHS = [
    "xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[2]/div[1]/div/button[2]",
    "xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[2]/div[1]/div/button[1]",
    "xpath=/html/body/div[1]/div[1]/div[5]/div/div/div/div/div[2]/div[1]/div/button[2]",
    "xpath=/html/body/div[1]/div[1]/div[5]/div/div/div/div/div[2]/div[1]/div/button[1]",
]
AGENT_ACTIVE_INDICATOR_XPATHS = [
    "xpath=/html/body/div[2]/div[1]/div[4]/div[1]/div[2]/div/div/div[2]/div[1]/div[1]/div[2]/button[2]",
    "xpath=/html/body/div[1]/div[1]/div[4]/div[1]/div[2]/div/div/div[2]/div[1]/div[1]/div[2]/button[2]",
    "xpath=/html/body/div[1]/div[1]/div[4]/div[2]/div[2]/div[1]/div[1]/div[2]/div[1]/div[1]/div[2]/button[2]",
]
AGENT_INSTRUCTIONS_BUTTON_TEXT = "Istruzioni agente"
AGENT_STRUMENTI_BUTTON_TEXT = "Strumenti"
AGENT_CLOSE_BUTTON_SELECTOR = 'button:has-text("Chiudi"), button:has-text("Close"), button[aria-label="Chiudi"], button[aria-label="Close"]'

APPROVE_BUTTON_SELECTORS = [
    'button:has-text("Approva")',
    'button:has-text("Approve")',
    'button:has-text("Genera")',
    'button:has-text("Generate")',
    'div[role="button"]:has-text("Approva")',
    'div[role="button"]:has-text("Approve")',
]

DONT_ASK_AGAIN_SELECTOR = 'label:has-text("Approva e non chiedermelo più"), span:has-text("Approva e non chiedermelo più"), label:has-text("Don\'t ask again")'

# ── Limiti temporali ───────────────────────────────────────────────────────────
IMAGE_WAIT_TIMEOUT = 80           # aumentato a 80s
POST_GOTO_WAIT = 15               # attesa dopo navigazione (versione stabile)
POST_ENTER_WAIT = 5               # aumentato a 5s
TYPE_DELAY_MS = 80                # delay tra caratteri durante digitazione
DOM_POLL_INTERVAL = 5             # secondi tra polling DOM

# ── Soglie contenuti ───────────────────────────────────────────────────────────
MIN_IMAGE_BODY_BYTES = 1000       # body minimo per network capture
MIN_DOM_IMAGE_BYTES = 2000        # body minimo per DOM capture
MAX_IMAGES = 4                    # numero massimo immagini da catturare

# ── URL pattern ────────────────────────────────────────────────────────────────
IMAGE_DOM_PATTERN = "googleusercontent.com"
IMAGE_DOM_PATTERN_FALLBACK = "google.com/imgres"
MEDIA_REDIRECT_PATTERN = "media.getMediaUrlRedirect"

# ── Logger ─────────────────────────────────────────────────────────────────────
log = logging.getLogger("AutomationEngine.FlowConfig")
