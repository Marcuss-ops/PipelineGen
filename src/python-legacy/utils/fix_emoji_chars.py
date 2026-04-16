#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Script per correggere i caratteri emoji problematici nel file Generatevideoparallelized.py
"""

import os
import re

def fix_emoji_characters():
    file_path = "Generatevideoparallelized.py"
    
    try:
        # Leggi il file
        with open(file_path, 'r', encoding='utf-8') as f:
            content = f.read()
        
        # Sostituzioni usando regex per caratteri problematici
        # Sostituisci sequenze di caratteri corrotti con emoji corretti
        content = re.sub(r'ðŸ§¹', '🧹', content)  # broom
        content = re.sub(r'ðŸ"', '📁', content)   # folder
        content = re.sub(r'ðŸ"Š', '📊', content)  # bar chart
        content = re.sub(r'ðŸ"„', '🔄', content)  # refresh
        content = re.sub(r'ðŸŽ¯', '🎯', content)  # target
        content = re.sub(r'ðŸŽ¬', '🎬', content)  # movie camera
        content = re.sub(r'ðŸŽµ', '🎵', content)  # musical note
        content = re.sub(r'ðŸ—.ï¸', '🗑️', content) # trash (pattern match)
        content = re.sub(r'ðŸ"´', '🔴', content)  # red circle
        content = re.sub(r'ðŸš€', '🚀', content)  # rocket
        content = re.sub(r'ðŸ"§', '🔧', content)  # wrench
        content = re.sub(r'âš.ï¸', '⚠️', content)  # warning (pattern match)
        content = re.sub(r'âœ…', '✅', content)   # check mark
        content = re.sub(r'âŒ', '❌', content)    # cross mark
        
        # Scrivi il file corretto
        with open(file_path, 'w', encoding='utf-8') as f:
            f.write(content)
        
        print("Caratteri emoji corretti in", file_path)
        
    except Exception as e:
        print("Errore durante la correzione:", e)

if __name__ == "__main__":
    fix_emoji_characters()