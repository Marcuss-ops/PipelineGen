#!/usr/bin/env python3
import sys
from pathlib import Path

# Add project root to sys.path so we can import packages
sys.path.insert(0, str(Path(__file__).resolve().parent.parent.parent))

# Expose internal modules/functions for tests and backward compatibility
from scripts.bridges.book_processor.cli import main
from scripts.bridges.book_processor.utils import clean_html, chunk_text, deduplicate_repetitions, clean_output
from scripts.bridges.book_processor.llm import translate_text_ollama, call_ollama
from scripts.bridges.book_processor.config import LANGUAGES

if __name__ == "__main__":
    main()