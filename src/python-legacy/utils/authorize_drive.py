#!/usr/bin/env python3
"""Script per autorizzare un account Google e ottenere token per Google Drive"""

import os
import sys
import json
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
    from Modules.youtube_uploader import _paths, _load_json, _select_best_credentials_file  # type: ignore
    from google_auth_oauthlib.flow import InstalledAppFlow
    from google.oauth2.credentials import Credentials

# Scope necessari per Google Drive
DRIVE_SCOPES = [
    'https://www.googleapis.com/auth/drive.file',  # Permette di creare/modificare file
    'https://www.googleapis.com/auth/drive'       # Permette accesso completo a Drive
]

def authorize_drive():
    """Autorizza un account Google per Google Drive"""
    print("=" * 70)
    print("🔐 AUTORIZZAZIONE GOOGLE DRIVE")
    print("=" * 70)
    print()
    
    # Ottieni il percorso delle credenziali
    base, cred, tokens_dir = _paths()
    
    # Seleziona il credentials.json migliore
    credentials_file = _select_best_credentials_file()
    
    if not os.path.exists(credentials_file):
        print(f"❌ File credentials non trovato: {credentials_file}")
        print(f"💡 Assicurati di avere un file credentials.json nella cartella Modules")
        return False
    
    print(f"📁 Usando credentials: {credentials_file}")
    print(f"📁 Directory token: {tokens_dir}")
    print()
    
    # Crea il flow OAuth2
    try:
        flow = InstalledAppFlow.from_client_secrets_file(
            credentials_file,
            DRIVE_SCOPES
        )
        
        print("🌐 Avvio del browser per l'autorizzazione...")
        print("   Verrai reindirizzato a Google per autorizzare l'accesso a Google Drive")
        print()
        
        # Esegui il flow locale (apre il browser)
        creds = flow.run_local_server(port=0)
        
        print()
        print("✅ Autorizzazione completata!")
        print()
        
        # Salva il token
        timestamp = int(datetime.now().timestamp() * 1000)
        token_file = os.path.join(tokens_dir, f"account_drive_{timestamp}.json")
        
        token_data = {
            'token': creds.token,
            'refresh_token': creds.refresh_token,
            'token_uri': creds.token_uri,
            'client_id': creds.client_id,
            'client_secret': creds.client_secret,
            'scopes': creds.scopes,
            'universe_domain': 'googleapis.com',
            'account': '',
            'expiry': creds.expiry.isoformat() if creds.expiry else None
        }
        
        with open(token_file, 'w') as f:
            json.dump(token_data, f, indent=2)
        
        print("=" * 70)
        print("✅ TOKEN SALVATO CON SUCCESSO!")
        print("=" * 70)
        print(f"📁 File token: {token_file}")
        print(f"🔑 Scopes: {', '.join(creds.scopes)}")
        print(f"⏰ Scade: {creds.expiry.isoformat() if creds.expiry else 'N/A'}")
        print("=" * 70)
        print()
        print("💡 Ora puoi usare questo token per caricare file su Google Drive")
        print()
        
        return True
        
    except Exception as e:
        print(f"❌ Errore durante l'autorizzazione: {e}")
        import traceback
        print()
        print("📋 Traceback:")
        print(traceback.format_exc())
        return False

if __name__ == "__main__":
    success = authorize_drive()
    sys.exit(0 if success else 1)

