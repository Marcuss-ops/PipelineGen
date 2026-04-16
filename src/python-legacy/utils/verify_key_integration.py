#!/usr/bin/env python3
"""
Verifica che la nuova chiave funzioni nel contesto del codice pollinations_ai.py.
"""

import os
import sys
import logging

# Imposta la chiave prima di importare il modulo
os.environ['POLLINATIONS_API_KEY'] = 'sk_YuAyvdUdG0BvEqONZALE7QmBhxcyiM6y'

logging.basicConfig(level=logging.INFO, format='%(levelname)s: %(message)s')
logger = logging.getLogger(__name__)

def test_key_integration():
    """Testa che la chiave funzioni quando importata dal modulo pollinations_ai."""
    logger.info("=" * 60)
    logger.info("🔍 VERIFICA INTEGRAZIONE CHIAVE API")
    logger.info("=" * 60)
    
    try:
        # Importa il modulo (caricherà la chiave)
        import pollinations_ai  # type: ignore
        
        logger.info(f"Chiave utilizzata dal modulo: {pollinations_ai.API_KEY[:8]}...{pollinations_ai.API_KEY[-4:]}")
        logger.info(f"Client disponibile: {'✅ Sì' if pollinations_ai.client else '❌ No'}")
        
        if not pollinations_ai.client:
            logger.error("❌ Client non inizializzato")
            return False
        
        # Testa una richiesta reale
        logger.info("\n🔸 Eseguo test request...")
        try:
            response = pollinations_ai.client.chat.completions.create(
                model="openai",
                messages=[{"role": "user", "content": "Rispondi solo 'OK'"}],
                max_tokens=10
            )
            
            if response and response.choices:
                content = response.choices[0].message.content
                logger.info(f"✅ SUCCESSO! Risposta: {content}")
                logger.info(f"\n✅ La nuova chiave funziona correttamente nel codice!")
                return True
            else:
                logger.error("❌ Risposta vuota")
                return False
                
        except Exception as e:
            logger.error(f"❌ Errore nella richiesta: {e}")
            return False
            
    except ImportError as e:
        logger.error(f"❌ Errore import: {e}")
        return False
    except Exception as e:
        logger.error(f"❌ Errore: {e}")
        return False

if __name__ == "__main__":
    success = test_key_integration()
    sys.exit(0 if success else 1)

