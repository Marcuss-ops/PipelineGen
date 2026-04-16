#!/usr/bin/env python3
"""
Funzione per verificare lo stato di Google Drive
"""

import os
import sys
from pathlib import Path

def check_google_drive_status():
    """Verifica se Google Drive è configurato e funzionante."""
    try:
        # Prova prima il percorso assoluto, poi quello relativo
        youtube_posting_path = Path("/home/pierone/Pyt/YoutubePosting")
        if not youtube_posting_path.exists():
            base_dir = Path(__file__).parent.parent.parent.parent
            youtube_posting_path = base_dir / "YoutubePosting"
        
        if not youtube_posting_path.exists():
            return False, "Directory YoutubePosting non trovata"
        
        sys.path.insert(0, str(youtube_posting_path))
        from Modules.youtube_uploader import _paths, _load_json  # type: ignore
        
        from google.oauth2.credentials import Credentials
        from google.auth.transport.requests import Request
        from googleapiclient.discovery import build
        
        # Ottieni i percorsi
        base, cred_file, tokens_dir = _paths()
        tokens_path = Path(tokens_dir)
        
        if not tokens_path.exists():
            return False, "Directory tokens non trovata"
        
        # Cerca SOLO token Drive (evita di provare token YouTube, che non hanno scope Drive e falsano il check)
        # Prova prima i più recenti (così un nuovo token "buono" viene scelto subito).
        token_files = sorted(list(tokens_path.glob("account_drive_*.json")), key=lambda p: p.stat().st_mtime, reverse=True)
        
        if not token_files:
            return False, f"Nessun token Google Drive trovato in {tokens_path}"
        
        # Log dettagliato per debug
        import logging
        logger = logging.getLogger(__name__)
        logger.info(f"📁 Trovati {len(token_files)} token Google Drive in {tokens_path}")
        
        # Prova i token fino a trovarne uno valido
        last_error = None
        for idx, token_file in enumerate(token_files[:10], 1):  # Prova i primi 10
            try:
                logger.info(f"🔍 Test token {idx}/{min(len(token_files), 10)}: {token_file.name}")
                token_data = _load_json(token_file, {})
                
                if not token_data.get('token') or not token_data.get('refresh_token'):
                    logger.warning(f"   ⚠️ Token incompleto (manca token o refresh_token)")
                    continue
                
                # Crea credenziali
                scopes = token_data.get("scopes") or ['https://www.googleapis.com/auth/drive.file']
                creds = Credentials(
                    token=token_data.get('token'),
                    refresh_token=token_data.get('refresh_token'),
                    token_uri=token_data.get('token_uri', 'https://oauth2.googleapis.com/token'),
                    client_id=token_data.get('client_id'),
                    client_secret=token_data.get('client_secret'),
                    scopes=scopes
                )
                
                # Refresh se necessario
                if creds.expired:
                    logger.info(f"   🔄 Token scaduto, refresh in corso...")
                    creds.refresh(Request())
                    logger.info(f"   ✅ Token refresh completato")
                
                # Testa il token
                logger.info(f"   🧪 Test accesso a Google Drive...")
                service = build('drive', 'v3', credentials=creds)
                service.files().list(pageSize=1, fields="nextPageToken, files(id, name)").execute()
                
                logger.info(f"   ✅ Token valido e funzionante!")
                return True, f"Token valido: {token_file.name}"
                
            except Exception as e:
                last_error = str(e)
                logger.warning(f"   ❌ Token non valido: {last_error[:120]}")
                continue
        
        error_msg = "Nessun token Google Drive valido trovato"
        if last_error:
            error_msg += f" (ultimo errore: {last_error[:100]})"
        if last_error and "deleted_client" in last_error:
            error_msg += ". Sembra che l'OAuth client sia stato eliminato: rigenera un token con `python VeloxEditing/refactored/authorize_drive.py`."
        return False, error_msg
        
    except ImportError as e:
        return False, f"Librerie Google non installate: {e}"
    except Exception as e:
        return False, f"Errore: {str(e)[:100]}"
