
from typing import Dict, Any, Type
from .base import BaseEntityHandler
from ..common.context import GenerationContext
from .frasi_importanti import FrasiImportantiHandler
from .numeri import NumeriHandler
from .parole_importanti import ParoleImportantiHandler
from .date import DateHandler
from .nomi_speciali import NomiSpecialiHandler
from .nomi_con_testo import NomiConTestoHandler

class EntityManager:
    def __init__(self, context: GenerationContext):
        self.context = context
        self.handlers: Dict[str, BaseEntityHandler] = {
            "Frasi_Importanti": FrasiImportantiHandler(context),
            "Numeri": NumeriHandler(context),
            "Parole_Importanti": ParoleImportantiHandler(context),
            "Date": DateHandler(context),
            "Nomi_Speciali": NomiSpecialiHandler(context),
            "Nomi_Con_Testo": NomiConTestoHandler(context)
        }

    def process_segment(self, category: str, segment: Dict[str, Any], idx: int) -> bool:
        handler = self.handlers.get(category)
        if not handler:
            self.context.status_callback(f"No handler for category '{category}'", True)
            return False
            
        return handler.process(segment, idx)
