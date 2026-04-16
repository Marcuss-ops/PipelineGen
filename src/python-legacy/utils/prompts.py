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

# Constants for Qwen web automation
TEXTAREA_SELECTOR = "textarea#chat-input, textarea[data-testid='chat-input'], textarea[aria-label*='chat'], textarea"
QWEN_CHAT_URL = "https://chat.qwen.ai/"


def _esegui_singola_query_qwen_selenium_sincrona(
    driver: webdriver.Edge,
    prompt_text: str,
    descrizione_task: str
) -> Dict[str, Any]:
    """
    Esegue una singola query a Qwen in modo sincrono.
    
    Args:
        driver: WebDriver instance
        prompt_text: Il prompt da inviare
        descrizione_task: Descrizione del task per logging
        
    Returns:
        Dict con status ("success" o "error") e data o error_message
    """
    try:
        # Trova la textarea
        text_area = attendi_textarea_pronta(driver, timeout=10)
        if not text_area:
            return {
                "status": "error",
                "error_message": "Textarea non trovata"
            }
        
        # Invia il prompt
        pyperclip.copy(prompt_text)
        text_area.send_keys(Keys.CONTROL, 'v')
        time.sleep(0.5)
        text_area.send_keys(Keys.ENTER)
        
        # Attendi e estrai la risposta
        time.sleep(random.uniform(3, 6))
        risposta = estrai_risposta(driver, descrizione_task)
        
        if not risposta:
            return {
                "status": "error",
                "error_message": "Nessuna risposta ricevuta"
            }
        
        # Prova a parsare come JSON
        try:
            # Rimuovi eventuali markdown code blocks
            import re
            json_match = re.search(r'```json\n(.*)\n```', risposta, re.DOTALL)
            if json_match:
                risposta = json_match.group(1)
            
            data = json.loads(risposta)
            return {
                "status": "success",
                "data": data
            }
        except json.JSONDecodeError:
            # Se non è JSON valido, restituisci come stringa
            return {
                "status": "success",
                "data": risposta
            }
            
    except Exception as e:
        return {
            "status": "error",
            "error_message": str(e)
        }

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



# PROMPT_QWEN_BASE rimosso - ora si usano i prompt specifici per ogni tipo di entità



def estrai_annotazioni_da_ai_sync(
    driver: webdriver.Edge,
    testo_trascritto: str,
    lingua: str,
    entita_immagini_json_input_str: str,
    max_retry_passes: int = 1
) -> Dict[str, Any]:
    """
    Estrae annotazioni grezze da un modello AI.

    Questa versione implementa una logica di invio del contesto modificata:
    1. Incolla il testo principale nella textarea.
    2. Incolla il prompt con il JSON delle entità nella stessa textarea.
    3. Invia il contenuto combinato.

    Gestisce anche popup, una logica di re-prompt per i risultati mancanti o errati,
    e il consumo della risposta di conferma iniziale.
    """
    qwen_main_label = "[AISyncOrchestrator]"
    logging.info(f"{qwen_main_label} Avvio estrazione annotazioni AI (max_retry_passes: {max_retry_passes})")
    start_time_orchestrator = time.time()

    # Struttura per i risultati finali
    final_qwen_results = {
        "Frasi_Importanti_Qwen": [],
        "Parole_Importanti_Qwen": [],
        "Associazione_Entita_Immagini_Qwen": {},
        "errors": {}
    }

    # Parsing del JSON delle entità in input
    json_entita_per_prompt_qwen = {}
    try:
        if entita_immagini_json_input_str and entita_immagini_json_input_str.strip():
            cleaned_json_str = entita_immagini_json_input_str.strip()
            if not cleaned_json_str or cleaned_json_str.isspace():
                logging.info(f"{qwen_main_label} JSON entità vuoto o solo spazi. Uso dict vuoto.")
                json_entita_per_prompt_qwen = {}
            else:
                json_entita_per_prompt_qwen = cleaned_json_str
        else:
            logging.info(f"{qwen_main_label} Nessun JSON entità fornito. Uso dict vuoto.")
            json_entita_per_prompt_qwen = {}
    except Exception as e_json_parse:
        logging.error(f"{qwen_main_label} Errore processing entita_immagini_json_input_str: {e_json_parse}. Input ricevuto: '{entita_immagini_json_input_str[:100]}...' Uso dict vuoto.")
        final_qwen_results["errors"]["initial_entity_json_parse"] = str(e_json_parse)
        json_entita_per_prompt_qwen = {}

    # Prepare text for prompts
    max_context_len = 18000
    testo_per_prompt = testo_trascritto[:max_context_len] + ('...' if len(testo_trascritto) > max_context_len else '')
    try:
        if QWEN_CHAT_URL not in driver.current_url:
            driver.get(QWEN_CHAT_URL)
            WebDriverWait(driver, 10).until(EC.presence_of_element_located((By.CSS_SELECTOR, TEXTAREA_SELECTOR)))

        chiudi_popup(driver)
        driver.switch_to.window(driver.current_window_handle)
        driver.execute_script("window.focus();")
        logging.info(f"{qwen_main_label} Finestra del browser attivata forzatamente.")
        # Ready to send prompts directly with text included
        logging.info(f"{qwen_main_label} Pronto per inviare i prompt con testo incluso...")
    except Exception as e:
        logging.error(f"{qwen_main_label} Errore durante l'accesso a Qwen: {e}")
        final_qwen_results["errors"]["access_error"] = str(e)
        return final_qwen_results

    # Prima invia il testo come contesto
    prompt_contesto = PROMPT_TESTO_COMPLETO.format(lingua_testo=lingua, testo=testo_per_prompt)
    logging.info(f"{qwen_main_label} Invio contesto iniziale...")
    try:
        context_response = _esegui_singola_query_qwen_selenium_sincrona(
            driver, prompt_contesto, "Invio Contesto"
        )
        if context_response["status"] != "success":
            logging.warning(f"{qwen_main_label} Errore nell'invio del contesto: {context_response.get('error_message')}")
        else:
            logging.info(f"{qwen_main_label} Contesto inviato con successo")
        time.sleep(random.uniform(2, 4))
    except Exception as e:
        logging.error(f"{qwen_main_label} Errore durante l'invio del contesto: {e}")

    tasks_definition = [
        ("Nomi Speciali", PROMPT_NOMI_SPECIALI, "Nomi_Speciali_Qwen", {"lingua_testo": lingua, "testo": testo_per_prompt, "NUMERO_ENTITA": NUMERO_ENTITA}, list),
        ("Frasi Importanti", PROMPT_FRASI_IMPORTANTI, "Frasi_Importanti_Qwen", {"lingua_testo": lingua, "NUMERO_ENTITA": NUMERO_ENTITA}, list),
        # Applica regola 5x per Parole Importanti nel prompt verso Qwen
        ("Parole Importanti", PROMPT_PAROLE_IMPORTANTI, "Parole_Importanti_Qwen", {"lingua_testo": lingua, "NUMERO_ENTITA": NUMERO_ENTITA + 5}, list),
        ("Entita Senza Testo (Link)", PROMPT_ENTITA_PER_Link, "Entita_Senza_Testo_Qwen",
         {"lingua_testo": lingua, "json_input": json_entita_per_prompt_qwen if isinstance(json_entita_per_prompt_qwen, str) else json.dumps(json_entita_per_prompt_qwen, ensure_ascii=False)}, dict),
        ("Associazione Entita con Immagini", PROMPT_ENTITA_PER_IMMAGINI, "Associazione_Entita_Immagini_Qwen", {"lingua_testo": lingua}, dict),
    ]

    for current_retry_pass in range(max_retry_passes + 1):
        if current_retry_pass > 0:
            logging.info(f"{qwen_main_label} === Inizio Passata di Re-Prompt #{current_retry_pass} ===")
        
        tasks_to_retry = []
        if current_retry_pass == 0:
            tasks_to_retry = tasks_definition
        else:
            for desc, prompt, key, ctx, type_ in tasks_definition:
                current_val = final_qwen_results.get(key)
                is_empty = (isinstance(current_val, list) and not current_val) or \
                           (isinstance(current_val, dict) and not current_val)
                has_error = key in final_qwen_results["errors"]

                if desc == "Entita Senza Testo (Link)" and isinstance(current_val, dict):
                    if has_error:
                        tasks_to_retry.append((desc, prompt, key, ctx, type_))
                elif has_error or not isinstance(current_val, type_) or is_empty:
                    tasks_to_retry.append((desc, prompt, key, ctx, type_))
            
            if tasks_to_retry:
                logging.info(f"{qwen_main_label} [Re-Prompt] Ci sono {len(tasks_to_retry)} task da ritentare. Re-invio del contesto...")
                try:
                    # Re-invia il contesto per i retry
                    context_retry_response = _esegui_singola_query_qwen_selenium_sincrona(
                        driver, prompt_contesto, "Re-invio Contesto"
                    )
                    if context_retry_response["status"] != "success":
                        logging.warning(f"{qwen_main_label} Errore nel re-invio del contesto: {context_retry_response.get('error_message')}")
                    else:
                        logging.info(f"{qwen_main_label} Contesto re-inviato con successo")
                    time.sleep(random.uniform(2, 4))
                except Exception as e_reprompt_ctx:
                    logging.error(f"{qwen_main_label} [Re-Prompt] Errore re-invio contesto: {e_reprompt_ctx}", exc_info=True)
                    final_qwen_results["errors"]["reprompt_context_error"] = str(e_reprompt_ctx)

        if not tasks_to_retry:
            logging.info(f"{qwen_main_label} Nessun task da ritentare. Interrompo il ciclo.")
            break

        for desc, prompt_template, result_key, prompt_context, expected_type in tasks_to_retry:
            if result_key in final_qwen_results["errors"]:
                del final_qwen_results["errors"][result_key]

            logging.info(f"{qwen_main_label} Esecuzione query per: {desc} (Passata: {current_retry_pass})")
            
            try:
                current_prompt_text = prompt_template.format(**prompt_context)
                qwen_response_struct = _esegui_singola_query_qwen_selenium_sincrona(
                    driver, current_prompt_text, desc
                )
                
                if qwen_response_struct["status"] == "success":
                    parsed_data = qwen_response_struct["data"]
                    
                    if desc == "Entita Senza Testo (Link)" and isinstance(parsed_data, str):
                        if any(keyword in parsed_data.lower() for keyword in ["memorizzato", "pronto", "ok", "confermo", "ricevuto"]):
                            logging.info(f"{qwen_main_label} Ricevuta conferma per {desc}: {parsed_data}")
                            final_qwen_results[result_key] = {}
                        else:
                             try:
                                 final_qwen_results[result_key] = json.loads(parsed_data)
                             except:
                                 final_qwen_results[result_key] = {}
                    elif isinstance(parsed_data, expected_type):
                        final_qwen_results[result_key] = parsed_data
                    else:
                        raise TypeError(f"Tipo dati errato. Atteso {expected_type}, ricevuto {type(parsed_data)}")
                else:
                    raise RuntimeError(qwen_response_struct.get("error_message", "Errore sconosciuto"))

            except Exception as e:
                logging.error(f"{qwen_main_label} Errore query Qwen per '{desc}': {e}", exc_info=True)
                final_qwen_results["errors"][result_key] = str(e)
                final_qwen_results.setdefault(result_key, [] if expected_type is list else {})
            
            time.sleep(random.uniform(3, 6))

    total_duration = time.time() - start_time_orchestrator
    logging.info(f"{qwen_main_label} Estrazione completata in {total_duration:.2f}s.")
    
    # Rimuovi le entità per il link che sono solo di passaggio
    if "Entita_Senza_Testo_Qwen" in final_qwen_results:
        del final_qwen_results["Entita_Senza_Testo_Qwen"]
        
    logging.info(f"{qwen_main_label} Risultati finali: {json.dumps(final_qwen_results, indent=2, ensure_ascii=False)}")
    
    return final_qwen_results
  
  

QWEN_CHAT_URL = "https://chat.qwen.ai/"
# Timeout configurabili
INPUT_POPUP_TIMEOUT   = 5    # per il popup pre-input
OUTPUT_POPUP_TIMEOUT  = 5    # per il popup pre-output
RESPONSE_TIMEOUT      = 60  # per l’attesa della risposta di Qwen
COPY_BUTTON_TIMEOUT   = 10   # per l'attesa del pulsante "Copy"
    
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
