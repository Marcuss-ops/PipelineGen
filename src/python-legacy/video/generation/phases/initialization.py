
"""
MODULE: Initialization Phase
DESCRIPTION:
This phase handles the initial setup of the video generation process before any processing begins.

RESPONSIBILITIES:
- Validate that critical input paths (audio, output) are valid.
- Create and clean the temporary directory for the session.
- Initialize global logging and tracking variables in the Context.
- Validate that output directories exist.

INTERACTIONS:
- Input: `GenerationContext` (reads config/paths).
- Output: Updates `ctx.temp_dir` and ensures environment is ready.
- Dependencies: `os`, `shutil`.
"""
import os
import shutil
import logging
from ..common.context import GenerationContext

class InitializationPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def run(self):
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} [InitPhase] Initializing...", False)
        
        # 1. Validate Inputs
        if not os.path.exists(ctx.audio_path):
            raise FileNotFoundError(f"Audio file not found: {ctx.audio_path}")
            
        # 2. Setup Temp Dir
        if not os.path.exists(ctx.temp_dir):
            os.makedirs(ctx.temp_dir, exist_ok=True)
            
        # 3. Clean Temp Files (if any previous run left garbage, optional)
        # We rely on context.temp_files_to_clean for cleanup at end, 
        # but here we might want to ensure clean state.
        
        # 4. Config Validation
        # Check output dir
        output_dir = os.path.dirname(ctx.output_path)
        if output_dir and not os.path.exists(output_dir):
            os.makedirs(output_dir, exist_ok=True)

        ctx.status_callback(f"{ctx.main_label} [InitPhase] Validation OK. Temp dir: {ctx.temp_dir}", False)
