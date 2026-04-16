"""Module for storing prompt templates for the VideoCollegaTanti application."""

import json
import time
import random
import logging
import asyncio
from typing import Dict, Any
from selenium import webdriver
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.remote.webelement import WebElement
import pyperclip

# Import functions from web_automation module
try:
    from modules.web.web_automation import (
        attendi_textarea_pronta,
        chiudi_popup,
        estrai_risposta
    )
except ImportError:
    try:
        from .web_automation import (  # type: ignore
            attendi_textarea_pronta,
            chiudi_popup,
            estrai_risposta
        )
    except ImportError:
        from web_automation import (  # type: ignore
            attendi_textarea_pronta,
            chiudi_popup,
            estrai_risposta
        )

# Constants for web automation
TEXTAREA_SELECTOR = "textarea#chat-input, textarea[data-testid='chat-input'], textarea[aria-label*='chat'], textarea"


PROMPT_ENTITA_PER_Link = """
🎯 Obiettivo: Associare entità-immagine al testo corretto.

Questo è un processo in due fasi. Esegui entrambe le fasi in ordine prima di rispondere.

---

Queste sono le entità che dovrai trovare nel testo. Memorizzale per la fase successiva.

- Lingua del testo: {lingua_testo}
- JSON delle entità da associare (entità originale → URL):
  {json_input}

🔹 **IMPORTANTE**: Per questa fase, rispondi SOLO con una semplice affermazione di conferma (es: "Ho memorizzato le entità", "Pronto per la fase successiva", "OK"). 
Non è necessario fornire una risposta elaborata o un JSON valido per il contesto - una semplice conferma è sufficiente.

"""

PROMPT_ENTITA_PER_IMMAGINI = """
**Fase 2: Associazione con il Testo**

Ora, basandoti sul testo che ti ho fornito in precedenza e sulle entità che hai appena analizzato, esegui le seguenti istruzioni:

1.  Per **ogni entità** dal JSON di input, trova la sua **esatta menzione nel testo** (nella lingua {lingua_testo}). Non tradurre il nome, ma trova come appare nel testo.
2.  Associa la menzione trovata nel testo all'**URL originale** corrispondente.
3.  **IMPORTANTE**: TUTTE le entità dal JSON di input devono essere incluse nel risultato finale, anche se non trovate esattamente nel testo.

Il tuo **UNICO OUTPUT** deve essere un **oggetto JSON** che mappa la menzione trovata nel testo all'URL.

Formato di output richiesto:
{{
  "Menzione Esatta Trovata Nel Testo 1": "url1",
  "Menzione Esatta Trovata Nel Testo 2": "url2",
  ...
}}

🛑 **Regole Finali**:
- Il JSON deve essere l'unica cosa nella tua risposta. Nessun commento o testo aggiuntivo.
- Mantieni gli URL originali.
- Le chiavi del JSON di output devono essere le parole/frasi esatte trovate nel testo.
- **OBBLIGATORIO**: Ogni entità dal JSON di input deve avere una corrispondenza nel JSON di output.
- Se un'entità non è trovata esattamente, usa la variante più simile presente nel testo.
"""

PROMPT_TESTO_COMPLETO = """
Dato il seguente testo in , analizzalo e memorizzalo ti servirà per rispondere alle domande successive RISPONDI CON UNA SEMPLICE AFFERMAZIONE {lingua_testo}:{testo}
\n\n
"""



# PROMPT_BASE rimosso - ora si usano i prompt specifici per ogni tipo di entità



NUMERO_ENTITA = 1
 
PROMPT_NOMI_SPECIALI = """
Analizza attentamente il seguente testo in {lingua_testo}. Il tuo obiettivo è estrarre i nomi speciali e le frasi più significative.


**Istruzioni:**
1. Identifica fino a {NUMERO_ENTITA} frasi particolarmente rilevanti presenti nel testo.Puoi anche inserire nomi che ritieni Rilevanti non solo frasi!
2. NON INSERIRE NESSUNA FRASE PROMISCUA O VOLGARE!

**Formato di Output:**
Restituisci il risultato ESCLUSIVAMENTE come un array JSON di stringhe. Non includere commenti, spiegazioni o testo aggiuntivo.

**Esempio di Output JSON Atteso:**
["Questa è una frase chiave importante", "L'intelligenza artificiale sta rivoluzionando il mondo.","Pablo Escobar"]

MAX {NUMERO_ENTITA} ENTITIES
"""

PROMPT_FRASI_IMPORTANTI = """
Analizza attentamente il testo fornito in precedenza in {lingua_testo}. Il tuo obiettivo è estrarre le frasi più significative e importanti.

**Istruzioni:**
1.  Identifica fino a {NUMERO_ENTITA} frasi che sono particolarmente rilevanti, informative o significative.
2.  Le frasi devono essere:
    *   **Complete e ben formate**: Evita frammenti o parti di frasi.
    *   **Significative**: Contengono informazioni chiave, concetti importanti o messaggi centrali.
    *   **Diverse tra loro**: Non ripetere concetti simili.
    *   **BREVI E CONCISE**: Ogni frase deve essere limitata a MASSIMO 5 RIGHE quando visualizzata (circa 100-125 caratteri per riga). Se una frase è troppo lunga, riducila mantenendo il significato essenziale.
3.  Escludi frasi troppo generiche, di saluto, o puramente descrittive senza contenuto sostanziale.
4. Non inserire le stesse presenti in Nomi Speciali
**Formato di Output:**
Restituisci il risultato ESCLUSIVAMENTE come un array JSON di stringhe. Non includere commenti, spiegazioni o testo aggiuntivo.

**Esempio di Output JSON Atteso:**
["La tecnologia blockchain sta rivoluzionando il settore finanziario.", "Il cambiamento climatico richiede azioni immediate e coordinate.", "L'intelligenza artificiale può migliorare significativamente l'efficienza aziendale."]

**IMPORTANTE**: Ogni frase deve essere limitata a massimo 5 righe quando visualizzata. Frasi troppo lunghe verranno troncate.

MAX {NUMERO_ENTITA} ENTITIES
"""

PROMPT_PAROLE_IMPORTANTI = """
Analizza attentamente il testo fornito in precedenza in {lingua_testo}.

Estrai un massimo di {NUMERO_ENTITA} parole chiave o termini singoli (non frasi) che sono più rilevanti per il testo,
concentrandoti su nomi propri, termini tecnici, concetti importanti e parole che catturano l'essenza del contenuto.
Evita parole comuni come articoli, preposizioni e congiunzioni.

Restituisci il risultato ESCLUSIVAMENTE come un array JSON di stringhe.
Esempio di output JSON atteso: ["blockchain", "sostenibilità", "innovazione", "mercato"]
MAX {NUMERO_ENTITA} ENTITIES
"""