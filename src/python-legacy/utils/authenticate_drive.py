#!/usr/bin/env python3
"""
Script per autenticarsi con Google Drive e salvare i token
"""

import os
import sys
import json
import argparse
from pathlib import Path
from datetime import datetime

# Aggiungi il percorso per i moduli
# Prova prima il percorso assoluto, poi quello relativo
youtube_posting_path = Path("/home/pierone/Pyt/YoutubePosting")
if not youtube_posting_path.exists():
    base_dir = Path(__file__).parent.parent.parent.parent
    youtube_posting_path = base_dir / "YoutubePosting"
if youtube_posting_path.exists():
    sys.path.insert(0, str(youtube_posting_path))
    from Modules.youtube_uploader import _paths, _load_json  # type: ignore

try:
    from google.oauth2.credentials import Credentials
    from google_auth_oauthlib.flow import InstalledAppFlow
    from google.auth.transport.requests import Request
    from googleapiclient.discovery import build
except ImportError as e:
    print(f"❌ Librerie Google non installate: {e}")
    print("   Installa con: pip install google-auth google-auth-oauthlib google-auth-httplib2 google-api-python-client")
    sys.exit(1)

# Scope per Google Drive
SCOPES = [
    'https://www.googleapis.com/auth/drive.file',
    'https://www.googleapis.com/auth/drive'  # Full access required for shared folders
]

def _run_console_like(flow: InstalledAppFlow):
    """
    Fallback per ambienti dove google-auth-oauthlib non espone run_console().
    Stampa un URL da aprire in un browser e chiede all'utente di incollare il codice
    oppure l'intero URL di redirect (che contiene ?code=...).
    """
    import urllib.parse

    # Assicura redirect_uri (alcune versioni non lo impostano automaticamente).
    if not getattr(flow, "redirect_uri", None):
        try:
            cfg = getattr(flow, "client_config", {}) or {}
            installed = cfg.get("installed") if isinstance(cfg, dict) else None
            redirects = (installed or {}).get("redirect_uris") if isinstance(installed, dict) else None
            if isinstance(redirects, list) and redirects:
                flow.redirect_uri = redirects[0]
        except Exception:
            pass
    # Manually set the redirect URI to match the approved one in Google Cloud Console
    # for Web Application credentials.
    # The script will prompt the user to paste the code derived from this redirect.
    flow.redirect_uri = "https://veloxmanager.duckdns.org/youtube_channels/oauth/callback"
    
    # OLD CODE:
    # if not getattr(flow, "redirect_uri", None):
    #     flow.redirect_uri = "http://localhost"

    # Forza selezione account (utile se nel browser è già loggato un altro account).
    prompt = "consent select_account"

    auth_url, _ = flow.authorization_url(
        access_type="offline",
        include_granted_scopes="true",
        prompt=prompt,
    )
    print("🌐 Apri questo URL nel browser (anche da un altro PC):")
    print(auth_url)
    print()
    print("Dopo l'autorizzazione, Google proverà a fare redirect a localhost.")
    print("Se vedi una pagina di errore, copia l'URL completo della barra (contiene ?code=...) e incollalo qui.")
    print("In alternativa incolla solo il codice 'code'.")
    print()
    user_input = input("Incolla qui URL di redirect o code: ").strip()
    if not user_input:
        raise RuntimeError("Input vuoto.")

    if user_input.startswith("http://") or user_input.startswith("https://"):
        # Prova ad estrarre il code dall'URL
        parsed = urllib.parse.urlparse(user_input)
        qs = urllib.parse.parse_qs(parsed.query or "")
        code = (qs.get("code") or [None])[0]
        if code:
            flow.fetch_token(code=code)
        else:
            # Come fallback, prova authorization_response se supportato
            flow.fetch_token(authorization_response=user_input)
    else:
        flow.fetch_token(code=user_input)

    return flow.credentials

def authenticate_drive(credentials_file: str = None, *, console: bool = False, open_browser: bool = True):
    """Autentica con Google Drive e salva i token."""
    print("=" * 70)
    print("🔐 AUTENTICAZIONE GOOGLE DRIVE")
    print("=" * 70)
    print()
    
    # Ottieni i percorsi
    try:
        base, cred_file, tokens_dir = _paths()
        print(f"📁 Directory base: {base}")
        print(f"📁 Directory tokens: {tokens_dir}")
        print()
    except Exception as e:
        print(f"❌ Errore nel trovare i percorsi: {e}")
        return False
    
    # Cerca tutti i file credentials disponibili
    credentials_files = []
    if os.path.exists(cred_file):
        credentials_files.append(cred_file)
    
    # Cerca anche credentials_*.json
    import glob
    pattern = os.path.join(base, "credentials_*.json")
    credentials_files.extend(glob.glob(pattern))
    credentials_files = sorted(set(credentials_files))  # Rimuovi duplicati e ordina
    
    if not credentials_files:
        print(f"❌ Nessun file credentials.json trovato in: {base}")
        print("   Crea un progetto su Google Cloud Console e scarica credentials.json")
        return False
    
    # Se è stato specificato un file, usalo
    if credentials_file:
        if os.path.exists(credentials_file):
            cred_file = credentials_file
        else:
            print(f"❌ File credentials specificato non trovato: {credentials_file}")
            return False
    # Se ci sono più file, chiedi all'utente quale usare (solo se interattivo)
    elif len(credentials_files) > 1:
        print("📄 File credentials disponibili:")
        for i, cf in enumerate(credentials_files, 1):
            print(f"   {i}. {os.path.basename(cf)}")
        print()
        
        # Controlla se siamo in modalità interattiva (stdin è un TTY)
        if sys.stdin.isatty():
            while True:
                try:
                    choice = input(f"Scegli quale file usare (1-{len(credentials_files)}) [default: 1]: ").strip()
                    if not choice:
                        choice = "1"
                    idx = int(choice) - 1
                    if 0 <= idx < len(credentials_files):
                        cred_file = credentials_files[idx]
                        break
                    else:
                        print(f"⚠️ Scelta non valida. Inserisci un numero tra 1 e {len(credentials_files)}")
                except ValueError:
                    print("⚠️ Inserisci un numero valido")
                except KeyboardInterrupt:
                    print("\n❌ Operazione annullata")
                    return False
        else:
            # Non interattivo, usa il primo
            print(f"⚠️ Modalità non interattiva, uso il primo file: {os.path.basename(credentials_files[0])}")
            cred_file = credentials_files[0]
    else:
        cred_file = credentials_files[0]
    
    print(f"📄 Usando file credentials: {cred_file}")
    print()
    
    # Carica le credenziali
    try:
        with open(cred_file, 'r') as f:
            creds_data = json.load(f)
        
        # Estrai client_id e client_secret
        if 'installed' in creds_data:
            client_config = creds_data['installed']
        elif 'web' in creds_data:
            client_config = creds_data['web']
        else:
            print("❌ Formato credentials.json non valido")
            return False
        
        client_id = client_config.get('client_id')
        client_secret = client_config.get('client_secret')
        
        if not client_id or not client_secret:
            print("❌ client_id o client_secret mancanti in credentials.json")
            return False
        
        print(f"✅ Credenziali caricate da: {cred_file}")
        print()
    except Exception as e:
        print(f"❌ Errore nel caricare credentials.json: {e}")
        return False
    
    # Crea la directory tokens se non esiste
    tokens_path = Path(tokens_dir)
    tokens_path.mkdir(parents=True, exist_ok=True)
    
    # Avvia il flow OAuth2
    try:
        print("🌐 Avvio autenticazione OAuth2...")
        print("   Si aprirà il browser per autorizzare l'accesso a Google Drive")
        print()
        
        flow = InstalledAppFlow.from_client_config(creds_data, SCOPES)

        if console:
            # Compat: alcune versioni non hanno run_console(); usiamo un fallback manuale.
            creds = _run_console_like(flow)
        else:
            creds = flow.run_local_server(port=0, open_browser=open_browser)
        
        print()
        print("✅ Autenticazione completata!")
        print()
        
        # Prepara i dati del token da salvare
        token_data = {
            'token': creds.token,
            'refresh_token': creds.refresh_token,
            'token_uri': creds.token_uri,
            'client_id': client_id,
            'client_secret': client_secret,
            'scopes': creds.scopes,
            'expiry': creds.expiry.isoformat() if creds.expiry else None
        }
        
        # Genera un nome file univoco per il token
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        token_filename = f"account_drive_{timestamp}.json"
        token_path = tokens_path / token_filename
        
        # Salva il token
        with open(token_path, 'w') as f:
            json.dump(token_data, f, indent=2)
        
        print(f"💾 Token salvato: {token_path}")
        print()
        
        # Testa il token
        print("🧪 Test del token...")
        try:
            service = build('drive', 'v3', credentials=creds)
            results = service.files().list(pageSize=1, fields="nextPageToken, files(id, name)").execute()
            print("✅ Token valido! Accesso a Google Drive confermato.")
            print()
            return True
        except Exception as test_err:
            print(f"⚠️ Token salvato ma test fallito: {test_err}")
            print("   Il token potrebbe essere valido comunque.")
            return True
        
    except Exception as e:
        print(f"❌ Errore durante l'autenticazione: {e}")
        import traceback
        print(traceback.format_exc())
        return False

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Autenticazione Google Drive")
    parser.add_argument(
        "--credentials",
        type=str,
        help="Percorso al file credentials.json da usare (opzionale)"
    )
    parser.add_argument(
        "--console",
        action="store_true",
        help="Esegue il flow in modalità console (utile su server senza browser).",
    )
    parser.add_argument(
        "--no-open-browser",
        action="store_true",
        help="Non apre automaticamente il browser (usa callback locale ma devi aprire tu l'URL).",
    )
    args = parser.parse_args()
    
    success = authenticate_drive(
        credentials_file=args.credentials,
        console=args.console,
        open_browser=not args.no_open_browser,
    )
    if success:
        print("=" * 70)
        print("✅ AUTENTICAZIONE COMPLETATA CON SUCCESSO!")
        print("=" * 70)
        print()
        print("💡 Il token è stato salvato e può essere usato per l'upload su Google Drive")
    else:
        print("=" * 70)
        print("❌ AUTENTICAZIONE FALLITA")
        print("=" * 70)
        sys.exit(1)
