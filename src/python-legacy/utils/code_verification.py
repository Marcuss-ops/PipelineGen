#!/usr/bin/env python3
"""
Code Verification Module
Verifica integrità del codice dopo il download:
- Compila tutti i file Python per verificare errori di sintassi
- Verifica import disponibili
- Controlla moduli mancanti
"""

import ast
import importlib.util
import sys
import os
from pathlib import Path
from typing import Dict, List, Tuple, Optional
import logging

logger = logging.getLogger(__name__)


def compile_python_file(file_path: Path) -> Tuple[bool, Optional[str]]:
    """
    Compila un file Python per verificare errori di sintassi.
    
    Returns:
        (success, error_message)
    """
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            source = f.read()
        
        # Compila per verificare sintassi
        compile(source, str(file_path), 'exec')
        return True, None
    except SyntaxError as e:
        return False, f"SyntaxError: {e.msg} at line {e.lineno}"
    except Exception as e:
        return False, f"Error: {str(e)}"


def check_import_availability(module_name: str, search_paths: List[Path]) -> Tuple[bool, Optional[str]]:
    """
    Verifica se un modulo può essere importato.
    
    Returns:
        (available, error_message)
    """
    try:
        # Prova import diretto
        try:
            __import__(module_name)
            return True, None
        except ImportError:
            pass
        
        # Prova a cercare il file nei path
        for search_path in search_paths:
            module_file = search_path / f"{module_name}.py"
            if module_file.exists():
                return True, None
            
            # Prova anche come package
            module_dir = search_path / module_name
            if module_dir.exists() and (module_dir / "__init__.py").exists():
                return True, None
        
        return False, f"Module '{module_name}' not found in search paths"
    except Exception as e:
        return False, f"Error checking import: {str(e)}"


def verify_code_integrity(code_dir: Path, report_missing_imports: bool = True) -> Dict[str, any]:
    """
    Verifica integrità del codice Python in una directory.
    
    Args:
        code_dir: Directory contenente il codice Python
        report_missing_imports: Se True, verifica anche gli import
    
    Returns:
        Dict con risultati della verifica:
        {
            "total_files": int,
            "compiled_successfully": int,
            "compilation_errors": List[Dict],
            "missing_imports": List[str],
            "available_imports": List[str],
            "success": bool
        }
    """
    code_dir = Path(code_dir)
    if not code_dir.exists():
        return {
            "success": False,
            "error": f"Directory non trovata: {code_dir}",
            "total_files": 0
        }
    
    results = {
        "total_files": 0,
        "compiled_successfully": 0,
        "compilation_errors": [],
        "missing_imports": [],
        "available_imports": [],
        "success": True
    }
    
    # Trova tutti i file Python
    python_files = list(code_dir.rglob("*.py"))
    results["total_files"] = len(python_files)
    
    logger.info(f"🔍 Verifica integrità codice in {code_dir}")
    logger.info(f"   📁 File Python trovati: {len(python_files)}")
    
    # Compila tutti i file
    for py_file in python_files:
        # Salta file in directory speciali
        if any(skip in py_file.parts for skip in ['__pycache__', '.git', 'venv', 'node_modules']):
            continue
        
        success, error = compile_python_file(py_file)
        if success:
            results["compiled_successfully"] += 1
        else:
            results["compilation_errors"].append({
                "file": str(py_file.relative_to(code_dir)),
                "error": error
            })
            results["success"] = False
            logger.warning(f"   ❌ {py_file.relative_to(code_dir)}: {error}")
    
    # Verifica import comuni se richiesto
    if report_missing_imports:
        logger.info(f"   🔍 Verifica import disponibili...")
        search_paths = [code_dir, code_dir.parent]
        
        # Lista di import comuni da verificare
        common_imports = [
            "modern_quotes_generator",
            "flickering_titles",
            "video_style_effects",
            "video_overlays",
            "video_processing",
            "audio_processing",
            "workflow",
            "standalone_multi_video",
            "job_worker",
            "release_manifest",
            "deployment_utils",
        ]
        
        # Aggiungi anche import da directory effetti se esiste
        effects_dir_candidates = [
            Path("/home/pierone/drive-download-20251216T145424Z-3-001"),
            Path("/opt/VeloxEditing/drive-download-20251216T145424Z-3-001"),
            code_dir.parent.parent / "drive-download-20251216T145424Z-3-001",
        ]
        
        for effects_dir in effects_dir_candidates:
            if effects_dir.exists():
                search_paths.append(effects_dir)
                # Cerca file Python nella directory effetti
                for py_file in effects_dir.glob("*.py"):
                    module_name = py_file.stem
                    if module_name not in common_imports:
                        common_imports.append(module_name)
                break  # Usa solo la prima directory effetti trovata
        
        for module_name in common_imports:
            available, error = check_import_availability(module_name, search_paths)
            if available:
                results["available_imports"].append(module_name)
            else:
                results["missing_imports"].append(module_name)
                logger.warning(f"   ⚠️ Import non disponibile: {module_name}")
    
    # Log riepilogo
    logger.info(f"   ✅ Compilati con successo: {results['compiled_successfully']}/{results['total_files']}")
    if results["compilation_errors"]:
        logger.warning(f"   ❌ Errori di compilazione: {len(results['compilation_errors'])}")
    if results["missing_imports"]:
        logger.warning(f"   ⚠️ Import mancanti: {len(results['missing_imports'])}")
    if results["available_imports"]:
        logger.info(f"   ✅ Import disponibili: {len(results['available_imports'])}")
    
    return results


def check_critical_modules(code_dir: Path) -> Dict[str, bool]:
    """
    Verifica che i moduli critici siano disponibili.
    
    Returns:
        Dict con {module_name: available}
    """
    code_dir = Path(code_dir)
    critical_modules = {
        "job_worker": code_dir / "job_worker.py",
        "standalone_multi_video": code_dir / "standalone_multi_video.py",
        "video_processing": code_dir / "video_processing.py",
        "video_overlays": code_dir / "video_overlays.py",
        "audio_processing": code_dir / "audio_processing.py",
        "workflow": code_dir / "workflow.py",
    }
    
    results = {}
    for module_name, module_path in critical_modules.items():
        exists = module_path.exists()
        results[module_name] = exists
        if not exists:
            logger.warning(f"   ⚠️ Modulo critico mancante: {module_name} ({module_path})")
    
    return results
